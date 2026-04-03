package licence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Licence is the domain model.
type Licence struct {
	LicenceID      string     `json:"licence_id"`
	DeploymentID   string     `json:"deployment_id"`
	StartsAt       time.Time  `json:"starts_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	GraceDays      int        `json:"grace_days"`
	Status         string     `json:"status"`
	NotifiedExpiry bool       `json:"notified_expiry"`
	NotifiedGrace  bool       `json:"notified_grace"`
	CreatedBy      *string    `json:"created_by"`
	RenewedBy      *string    `json:"renewed_by"`
	RenewedAt      *time.Time `json:"renewed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Repository handles licence SQL operations.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, deploymentID string, startsAt, expiresAt time.Time, graceDays int, createdBy string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO admin_svc.licences(deployment_id, starts_at, expires_at, grace_days, status, created_by)
		 VALUES($1,$2,$3,$4,'ACTIVE',$5) RETURNING licence_id::text`,
		deploymentID, startsAt, expiresAt, graceDays, uuidVal(createdBy)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("licence.Create: %w", err)
	}
	return id, nil
}

func (r *Repository) Renew(ctx context.Context, licenceID string, newExpiresAt time.Time, renewedBy string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.licences
		 SET expires_at=$2, status='ACTIVE', renewed_by=$3, renewed_at=now(), updated_at=now(),
		     notified_expiry=false, notified_grace=false
		 WHERE licence_id=$1`, licenceID, newExpiresAt, uuidVal(renewedBy))
	return err
}

const licenceSelect = `
	SELECT l.licence_id::text, l.deployment_id::text, l.starts_at, l.expires_at, l.grace_days, l.status,
	       l.notified_expiry, l.notified_grace,
	       COALESCE(uc.username, CASE WHEN l.created_by IS NULL THEN 'MASTER' END),
	       COALESCE(ur.username, CASE WHEN l.renewed_by IS NULL AND l.renewed_at IS NOT NULL THEN 'MASTER' END),
	       l.renewed_at, l.created_at, l.updated_at
	FROM admin_svc.licences l
	LEFT JOIN admin_svc.users uc ON uc.user_id = l.created_by
	LEFT JOIN admin_svc.users ur ON ur.user_id = l.renewed_by`

func (r *Repository) GetByDeployment(ctx context.Context, deploymentID string) ([]*Licence, error) {
	rows, err := r.pool.Query(ctx,
		licenceSelect+` WHERE l.deployment_id=$1 ORDER BY l.created_at DESC`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLicences(rows)
}

func (r *Repository) List(ctx context.Context, status string) ([]*Licence, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = r.pool.Query(ctx, licenceSelect+` ORDER BY l.created_at DESC`)
	} else {
		rows, err = r.pool.Query(ctx, licenceSelect+` WHERE l.status=$1 ORDER BY l.created_at DESC`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLicences(rows)
}

func scanLicences(rows pgx.Rows) ([]*Licence, error) {
	var out []*Licence
	for rows.Next() {
		l := &Licence{}
		if err := rows.Scan(&l.LicenceID, &l.DeploymentID, &l.StartsAt, &l.ExpiresAt, &l.GraceDays, &l.Status,
			&l.NotifiedExpiry, &l.NotifiedGrace, &l.CreatedBy, &l.RenewedBy, &l.RenewedAt,
			&l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// uuidVal returns nil when s is empty or the sentinel "MASTER".
func uuidVal(s string) any {
	if s == "" || s == "MASTER" {
		return nil
	}
	return s
}
