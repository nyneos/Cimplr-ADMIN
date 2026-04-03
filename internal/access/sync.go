package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncResult summarises one deployment sync operation.
type SyncResult struct {
	DeploymentID string `json:"deployment_id"`
	CompanyName  string `json:"company_name"`
	PermsSynced  int    `json:"perms_synced"`
	LicenceSync  bool   `json:"licence_sync"`
	Error        string `json:"error,omitempty"`
}

// SyncPermissionsToDeployment connects to the deployment's own database
// and writes the full permission + licence snapshot into its config schema.
//
// This is called:
//   - after AssignPackage
//   - after BulkUpsertPermissions
//   - after licence create / renew
//   - on-demand via POST /cimplrADMIN/access/deployment/sync
func SyncPermissionsToDeployment(ctx context.Context, adminPool *pgxpool.Pool, deploymentID string) (*SyncResult, error) {
	// ── 1. Load deployment DB credentials ────────────────────────────────────
	var dsn struct {
		CompanyName string
		DBUser      string
		DBPassword  string
		DBHost      string
		DBPort      string
		DBName      string
		IsActive    bool
	}
	err := adminPool.QueryRow(ctx,
		`SELECT company_name, db_user, db_password, db_host, db_port, db_name, is_active
		 FROM admin_svc.deployments WHERE deployment_id=$1`, deploymentID).
		Scan(&dsn.CompanyName, &dsn.DBUser, &dsn.DBPassword,
			&dsn.DBHost, &dsn.DBPort, &dsn.DBName, &dsn.IsActive)
	if err != nil {
		return nil, fmt.Errorf("deployment not found: %w", err)
	}

	// ── 2. Load all permissions for this deployment's assigned packages ───────
	type permRow struct {
		Module    string
		SubModule string
		Action    string
		IsAllowed bool
	}
	permRows, err := adminPool.Query(ctx,
		`SELECT pp.module, pp.sub_module, pp.action, pp.is_allowed
		 FROM admin_svc.package_permissions pp
		 JOIN admin_svc.deployment_packages dp ON dp.package_id = pp.package_id
		 WHERE dp.deployment_id = $1`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("loading permissions: %w", err)
	}
	defer permRows.Close()
	var perms []permRow
	for permRows.Next() {
		var p permRow
		if err := permRows.Scan(&p.Module, &p.SubModule, &p.Action, &p.IsAllowed); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}

	// ── 3. Load current licence status ───────────────────────────────────────
	var licenceStatus string
	var expiresAt string
	_ = adminPool.QueryRow(ctx,
		`SELECT status, expires_at::text
		 FROM admin_svc.licences
		 WHERE deployment_id=$1
		 ORDER BY expires_at DESC LIMIT 1`, deploymentID).
		Scan(&licenceStatus, &expiresAt)
	if licenceStatus == "" {
		licenceStatus = "NONE"
	}

	// ── 4. Connect to the deployment's own database ───────────────────────────
	clientDSN := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=require",
		dsn.DBUser, dsn.DBPassword, dsn.DBHost, dsn.DBPort, dsn.DBName,
	)
	clientPool, err := pgxpool.New(ctx, clientDSN)
	if err != nil {
		return &SyncResult{
			DeploymentID: deploymentID,
			CompanyName:  dsn.CompanyName,
			Error:        fmt.Sprintf("cannot connect to client DB: %v", err),
		}, nil
	}
	defer clientPool.Close()

	// ── 5. Ensure config schema + tables exist in client DB ───────────────────
	// These are flat tables — no foreign keys, no references to admin_svc.
	// They are owned by CimplrAdmin's sync process; the client app only reads them.
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS config`,
		`CREATE TABLE IF NOT EXISTS config.permissions (
			module      TEXT NOT NULL,
			sub_module  TEXT NOT NULL DEFAULT 'default',
			action      TEXT NOT NULL,
			is_allowed  BOOLEAN NOT NULL DEFAULT FALSE,
			synced_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(module, sub_module, action)
		)`,
		// Migrate existing permissions tables that were created before synced_at was added
		`ALTER TABLE config.permissions ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
		`CREATE TABLE IF NOT EXISTS config.settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			synced_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// Migrate existing settings tables that were created before synced_at was added
		`ALTER TABLE config.settings ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
	} {
		if _, err := clientPool.Exec(ctx, ddl); err != nil {
			return &SyncResult{
				DeploymentID: deploymentID,
				CompanyName:  dsn.CompanyName,
				Error:        fmt.Sprintf("cannot create config schema: %v", err),
			}, nil
		}
	}

	// ── 6. Write permissions in a single transaction ──────────────────────────
	tx, err := clientPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Clear existing permissions and replace with current snapshot
	if _, err := tx.Exec(ctx, `DELETE FROM config.permissions`); err != nil {
		return nil, fmt.Errorf("clearing permissions: %w", err)
	}
	for _, p := range perms {
		_, err := tx.Exec(ctx,
			`INSERT INTO config.permissions(module, sub_module, action, is_allowed, synced_at)
			 VALUES($1,$2,$3,$4,now())
			 ON CONFLICT(module, sub_module, action)
			 DO UPDATE SET is_allowed=EXCLUDED.is_allowed, synced_at=now()`,
			p.Module, p.SubModule, p.Action, p.IsAllowed)
		if err != nil {
			return nil, fmt.Errorf("inserting permission %s/%s/%s: %w", p.Module, p.SubModule, p.Action, err)
		}
	}

	// ── 7. Write licence + deployment status settings ─────────────────────────
	settings := map[string]string{
		"licence_status":      licenceStatus,
		"licence_expires_at":  expiresAt,
		"deployment_is_active": fmt.Sprintf("%v", dsn.IsActive),
	}
	for k, v := range settings {
		_, err := tx.Exec(ctx,
			`INSERT INTO config.settings(key, value, synced_at)
			 VALUES($1,$2,now())
			 ON CONFLICT(key)
			 DO UPDATE SET value=EXCLUDED.value, synced_at=now()`,
			k, v)
		if err != nil {
			return nil, fmt.Errorf("writing setting %s: %w", k, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &SyncResult{
		DeploymentID: deploymentID,
		CompanyName:  dsn.CompanyName,
		PermsSynced:  len(perms),
		LicenceSync:  true,
	}, nil
}

// SyncAllDeployments syncs every active deployment — called by the licence_checker
// after any status transition and can be triggered manually.
func SyncAllDeployments(ctx context.Context, adminPool *pgxpool.Pool) ([]SyncResult, error) {
	rows, err := adminPool.Query(ctx,
		`SELECT deployment_id::text FROM admin_svc.deployments WHERE status='APPROVED'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	var results []SyncResult
	for _, id := range ids {
		res, err := SyncPermissionsToDeployment(ctx, adminPool, id)
		if err != nil {
			results = append(results, SyncResult{DeploymentID: id, Error: err.Error()})
			continue
		}
		results = append(results, *res)
	}
	return results, nil
}
