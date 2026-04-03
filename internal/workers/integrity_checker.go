package workers

// integrity_checker.go
//
// Periodically connects to every active client deployment's own database and
// compares its config.permissions table against the authoritative values stored
// in admin_svc.package_permissions.
//
// If a client has tampered with a permission (e.g. flipped is_allowed=false
// to true for a row we set false), the checker:
//   1. Reverts the row back to the correct value.
//   2. Raises an admin_svc.alerts record (severity=CRITICAL).
//   3. Writes an admin_svc.audit_log entry.
//
// Config env vars:
//   INTEGRITY_CHECKER_ENABLED     true/false (default true)
//   INTEGRITY_CHECKER_POLL_MINS   poll interval in minutes (default 60)

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartIntegrityChecker launches the background integrity checker loop.
func StartIntegrityChecker(ctx context.Context, pool *pgxpool.Pool) {
	if v := os.Getenv("INTEGRITY_CHECKER_ENABLED"); v == "false" {
		log.Println("[integrity_checker] disabled via env")
		return
	}
	pollMins := 60
	if v := os.Getenv("INTEGRITY_CHECKER_POLL_MINS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollMins = n
		}
	}

	log.Printf("[integrity_checker] started (poll=%dm)", pollMins)
	ticker := time.NewTicker(time.Duration(pollMins) * time.Minute)
	defer ticker.Stop()

	// Run immediately on startup
	runIntegrityCheck(ctx, pool)

	for {
		select {
		case <-ctx.Done():
			log.Println("[integrity_checker] stopped")
			return
		case <-ticker.C:
			runIntegrityCheck(ctx, pool)
		}
	}
}

// runIntegrityCheck iterates every APPROVED deployment and checks each one.
func runIntegrityCheck(ctx context.Context, pool *pgxpool.Pool) {
	workerAudit(pool, "SYSTEM", "integrity_checker", "CHECK_STARTED", nil, map[string]any{"time": time.Now().UTC()})
	log.Println("[integrity_checker] running checks")

	type deploymentRow struct {
		ID          string
		CompanyName string
		DBUser      string
		DBPassword  string
		DBHost      string
		DBPort      string
		DBName      string
	}

	rows, err := pool.Query(ctx,
		`SELECT deployment_id::text, company_name, db_user, db_password, db_host, db_port, db_name
		 FROM admin_svc.deployments
		 WHERE status='APPROVED' AND is_active=true`)
	if err != nil {
		log.Printf("[integrity_checker] query deployments: %v", err)
		return
	}
	var deployments []deploymentRow
	for rows.Next() {
		var d deploymentRow
		if err := rows.Scan(&d.ID, &d.CompanyName, &d.DBUser, &d.DBPassword, &d.DBHost, &d.DBPort, &d.DBName); err != nil {
			log.Printf("[integrity_checker] scan: %v", err)
			continue
		}
		deployments = append(deployments, d)
	}
	rows.Close()

	for _, d := range deployments {
		checkDeployment(ctx, pool, d.ID, d.CompanyName,
			fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require&connect_timeout=10",
				d.DBUser, d.DBPassword, d.DBHost, d.DBPort, d.DBName))
	}
	log.Printf("[integrity_checker] done — checked %d deployment(s)", len(deployments))
	workerAudit(pool, "SYSTEM", "integrity_checker", "CHECK_COMPLETED",
		nil, map[string]any{"deployments_checked": len(deployments), "time": time.Now().UTC()})
}

type permKey struct {
	Module    string
	SubModule string
	Action    string
}

// checkDeployment connects to one client DB, compares permissions, fixes + alerts on mismatch.
func checkDeployment(ctx context.Context, adminPool *pgxpool.Pool, deploymentID, companyName, clientDSN string) {
	// ── 1. Load authoritative permissions from admin_svc ─────────────────────
	authRows, err := adminPool.Query(ctx,
		`SELECT pp.module, pp.sub_module, pp.action, pp.is_allowed
		 FROM admin_svc.package_permissions pp
		 JOIN admin_svc.deployment_packages dp ON dp.package_id = pp.package_id
		 WHERE dp.deployment_id = $1`, deploymentID)
	if err != nil {
		log.Printf("[integrity_checker] %s: load auth perms: %v", companyName, err)
		return
	}
	authoritative := map[permKey]bool{}
	for authRows.Next() {
		var m, sm, a string
		var allowed bool
		if err := authRows.Scan(&m, &sm, &a, &allowed); err != nil {
			continue
		}
		authoritative[permKey{m, sm, a}] = allowed
	}
	authRows.Close()

	if len(authoritative) == 0 {
		return // no package assigned yet — nothing to check
	}

	// ── 2. Connect to client DB ───────────────────────────────────────────────
	connCtx, connCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connCancel()

	clientPool, err := pgxpool.New(connCtx, clientDSN)
	if err != nil {
		log.Printf("[integrity_checker] %s: connect: %v", companyName, err)
		return
	}
	defer clientPool.Close()

	// ── 3. Read current client permissions ───────────────────────────────────
	clientRows, err := clientPool.Query(ctx,
		`SELECT module, sub_module, action, is_allowed FROM config.permissions`)
	if err != nil {
		// Table may not exist yet (first sync pending) — not an error
		log.Printf("[integrity_checker] %s: read client perms (skip): %v", companyName, err)
		return
	}
	client := map[permKey]bool{}
	for clientRows.Next() {
		var m, sm, a string
		var allowed bool
		if err := clientRows.Scan(&m, &sm, &a, &allowed); err != nil {
			continue
		}
		client[permKey{m, sm, a}] = allowed
	}
	clientRows.Close()

	// ── 4. Find tampered rows ─────────────────────────────────────────────────
	type tamper struct {
		Key            permKey
		AuthValue      bool // what admin says it should be
		ClientValue    bool // what client has
	}
	var tampers []tamper
	for k, authAllowed := range authoritative {
		clientAllowed, exists := client[k]
		if !exists {
			continue // missing row — will be fixed on next sync, not a tamper
		}
		if clientAllowed != authAllowed {
			tampers = append(tampers, tamper{k, authAllowed, clientAllowed})
		}
	}

	if len(tampers) == 0 {
		workerAudit(adminPool, "DEPLOYMENT", deploymentID, "INTEGRITY_CHECK_CLEAN",
			nil, map[string]any{"company": companyName, "perms_checked": len(authoritative)})
		return // clean
	}

	log.Printf("[integrity_checker] %s: %d tampered permission(s) detected — fixing", companyName, len(tampers))

	// ── 5. Fix tampered rows in client DB ─────────────────────────────────────
	fixCtx, fixCancel := context.WithTimeout(ctx, 30*time.Second)
	defer fixCancel()

	for _, t := range tampers {
		_, err := clientPool.Exec(fixCtx,
			`UPDATE config.permissions
			 SET is_allowed=$4, synced_at=now()
			 WHERE module=$1 AND sub_module=$2 AND action=$3`,
			t.Key.Module, t.Key.SubModule, t.Key.Action, t.AuthValue)
		if err != nil {
			log.Printf("[integrity_checker] %s: fix %s/%s/%s: %v",
				companyName, t.Key.Module, t.Key.SubModule, t.Key.Action, err)
		}
	}

	// ── 6. Raise one alert per deployment (batch detail) ─────────────────────
	alertCtx, alertCancel := context.WithTimeout(ctx, 10*time.Second)
	defer alertCancel()

	type tamperDetail struct {
		Module    string `json:"module"`
		SubModule string `json:"sub_module"`
		Action    string `json:"action"`
		Expected  bool   `json:"expected"`
		Found     bool   `json:"found"`
	}
	var details []tamperDetail
	for _, t := range tampers {
		details = append(details, tamperDetail{
			Module:    t.Key.Module,
			SubModule: t.Key.SubModule,
			Action:    t.Key.Action,
			Expected:  t.AuthValue,
			Found:     t.ClientValue,
		})
	}
	detailJSON, _ := json.Marshal(map[string]any{
		"tampered_count": len(tampers),
		"items":          details,
	})

	title := fmt.Sprintf("%s: %d permission(s) were tampered and have been restored", companyName, len(tampers))
	_, err = adminPool.Exec(alertCtx,
		`INSERT INTO admin_svc.alerts(alert_type, severity, deployment_id, title, detail)
		 VALUES('PERMISSION_TAMPERED','CRITICAL',$1,$2,$3)`,
		deploymentID, title, detailJSON)
	if err != nil {
		log.Printf("[integrity_checker] %s: create alert: %v", companyName, err)
	}

	// ── 7. Write audit log entry ──────────────────────────────────────────────
	auditDetail, _ := json.Marshal(map[string]any{
		"tampered_count": len(tampers),
		"items":          details,
		"action":         "auto_reverted",
	})
	_, _ = adminPool.Exec(alertCtx,
		`INSERT INTO admin_svc.audit_log(entity_type, entity_id, action, actor_role, new_value)
		 VALUES('DEPLOYMENT',$1,'PERMISSION_TAMPERED_AUTO_REVERTED','SYSTEM',$2)`,
		deploymentID, auditDetail)

	log.Printf("[integrity_checker] %s: alert raised + audit logged (%d tampered)", companyName, len(tampers))
}
