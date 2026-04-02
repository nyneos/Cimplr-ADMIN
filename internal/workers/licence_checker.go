package workers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"CimplrCorpSaas/admin/internal/access"
	"CimplrCorpSaas/admin/internal/notification"
)

// StartLicenceChecker runs the licence expiry/grace/suspension checks on a ticker.
func StartLicenceChecker(ctx context.Context, pool *pgxpool.Pool, pollHours int) {
	ticker := time.NewTicker(time.Duration(pollHours) * time.Hour)
	defer ticker.Stop()
	log.Printf("[licence_checker] started (poll=%dh)", pollHours)

	// Run immediately on start
	runLicenceCheck(ctx, pool)

	for {
		select {
		case <-ctx.Done():
			log.Println("[licence_checker] stopped")
			return
		case <-ticker.C:
			runLicenceCheck(ctx, pool)
		}
	}
}

func runLicenceCheck(ctx context.Context, pool *pgxpool.Pool) {
	dbCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	log.Println("[licence_checker] running checks")
	if err := stepExpiryWarning(dbCtx, pool); err != nil {
		log.Printf("[licence_checker] expiry_warning: %v", err)
	}
	if err := stepGracePeriod(dbCtx, pool); err != nil {
		log.Printf("[licence_checker] grace_period: %v", err)
	}
	if err := stepFullSuspension(dbCtx, pool); err != nil {
		log.Printf("[licence_checker] suspension: %v", err)
	}
}

// stepExpiryWarning notifies when licence expires within 7 days.
func stepExpiryWarning(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`SELECT licence_id::text, deployment_id::text, expires_at
		 FROM admin_svc.licences
		 WHERE status = 'ACTIVE'
		   AND expires_at <= now() + interval '7 days'
		   AND notified_expiry = false
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type licRow struct {
		LicenceID    string
		DeploymentID string
		ExpiresAt    time.Time
	}
	var lic []licRow
	for rows.Next() {
		var l licRow
		if err := rows.Scan(&l.LicenceID, &l.DeploymentID, &l.ExpiresAt); err != nil {
			return err
		}
		lic = append(lic, l)
	}
	rows.Close()

	for _, l := range lic {
		companyName, users, err := getDeploymentUsers(ctx, pool, l.DeploymentID)
		if err != nil {
			log.Printf("[licence_checker] expiry_warning get users: %v", err)
			continue
		}
		for _, u := range users {
			_ = notification.Enqueue(ctx, pool, notification.EnqueueRequest{
				EventID:        "LICENCE_EXPIRY_WARNING",
				RecipientEmail: u.Email,
				RecipientName:  u.Name,
				TemplateData: map[string]string{
					"Name":        u.Name,
					"CompanyName": companyName,
					"ExpiresAt":   l.ExpiresAt.Format("2006-01-02"),
				},
			})
		}
		_, _ = pool.Exec(ctx,
			`UPDATE admin_svc.licences SET notified_expiry=true, updated_at=now() WHERE licence_id=$1`,
			l.LicenceID)
	}
	return nil
}

// stepGracePeriod transitions expired-but-not-yet-graced licences to GRACE.
func stepGracePeriod(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`SELECT licence_id::text, deployment_id::text, expires_at, grace_days
		 FROM admin_svc.licences
		 WHERE status = 'ACTIVE'
		   AND expires_at < now()
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type licRow struct {
		LicenceID    string
		DeploymentID string
		ExpiresAt    time.Time
		GraceDays    int
	}
	var lic []licRow
	for rows.Next() {
		var l licRow
		if err := rows.Scan(&l.LicenceID, &l.DeploymentID, &l.ExpiresAt, &l.GraceDays); err != nil {
			return err
		}
		lic = append(lic, l)
	}
	rows.Close()

	for _, l := range lic {
		_, _ = pool.Exec(ctx,
			`UPDATE admin_svc.licences
			 SET status='GRACE', notified_grace=true, updated_at=now()
			 WHERE licence_id=$1`, l.LicenceID)

		// Push updated licence status to the deployment's own DB.
		go func(did string) { access.SyncPermissionsToDeployment(context.Background(), pool, did) }(l.DeploymentID) //nolint:errcheck

		companyName, users, err := getDeploymentUsers(ctx, pool, l.DeploymentID)
		if err != nil {
			continue
		}
		for _, u := range users {
			_ = notification.Enqueue(ctx, pool, notification.EnqueueRequest{
				EventID:        "LICENCE_GRACE_WARNING",
				RecipientEmail: u.Email,
				RecipientName:  u.Name,
				TemplateData: map[string]string{
					"Name":        u.Name,
					"CompanyName": companyName,
					"GraceDays":   fmt.Sprintf("%d", l.GraceDays),
				},
			})
		}
	}
	return nil
}

// stepFullSuspension expires licences that have exhausted their grace period.
func stepFullSuspension(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`SELECT licence_id::text, deployment_id::text
		 FROM admin_svc.licences
		 WHERE status = 'GRACE'
		   AND expires_at + (grace_days || ' days')::interval < now()
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type licRow struct {
		LicenceID    string
		DeploymentID string
	}
	var lic []licRow
	for rows.Next() {
		var l licRow
		if err := rows.Scan(&l.LicenceID, &l.DeploymentID); err != nil {
			return err
		}
		lic = append(lic, l)
	}
	rows.Close()

	for _, l := range lic {
		// Expire licence
		_, _ = pool.Exec(ctx,
			`UPDATE admin_svc.licences SET status='EXPIRED', updated_at=now() WHERE licence_id=$1`,
			l.LicenceID)

		// Suspend deployment
		_, _ = pool.Exec(ctx,
			`UPDATE admin_svc.deployments SET is_active=false, updated_at=now() WHERE deployment_id=$1`,
			l.DeploymentID)

		// Push EXPIRED status to the deployment's own DB.
		go func(did string) { access.SyncPermissionsToDeployment(context.Background(), pool, did) }(l.DeploymentID) //nolint:errcheck

		// Audit
		_, _ = pool.Exec(ctx,
			`INSERT INTO admin_svc.audit_log(entity_type,entity_id,action,new_value)
			 VALUES('LICENCE',$1,'EXPIRED',$2)`,
			l.LicenceID, map[string]string{"reason": "grace_period_exhausted"})

		companyName, users, err := getDeploymentUsers(ctx, pool, l.DeploymentID)
		if err != nil {
			continue
		}
		for _, u := range users {
			_ = notification.Enqueue(ctx, pool, notification.EnqueueRequest{
				EventID:        "LICENCE_EXPIRED",
				RecipientEmail: u.Email,
				RecipientName:  u.Name,
				TemplateData: map[string]string{
					"Name":        u.Name,
					"CompanyName": companyName,
				},
			})
		}
	}
	return nil
}

type deploymentUser struct {
	Email  string
	Name   string
	UserID string
}

// getDeploymentUsers returns the company name and all APPROVED users for a deployment.
func getDeploymentUsers(ctx context.Context, pool *pgxpool.Pool, deploymentID string) (string, []deploymentUser, error) {
	var companyName string
	err := pool.QueryRow(ctx,
		`SELECT company_name FROM admin_svc.deployments WHERE deployment_id=$1`,
		deploymentID).Scan(&companyName)
	if err != nil {
		return "", nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT email, COALESCE(full_name,''), user_id::text
		 FROM admin_svc.users WHERE status='APPROVED'`)
	if err != nil {
		return companyName, nil, err
	}
	defer rows.Close()

	var users []deploymentUser
	for rows.Next() {
		var u deploymentUser
		if err := rows.Scan(&u.Email, &u.Name, &u.UserID); err != nil {
			return companyName, nil, err
		}
		users = append(users, u)
	}
	return companyName, users, rows.Err()
}
