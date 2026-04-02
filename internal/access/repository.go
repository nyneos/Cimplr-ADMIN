package access

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Package represents an access package.
type Package struct {
	PackageID   string    `json:"package_id"`
	PackageCode string    `json:"package_code"`
	DisplayName string    `json:"display_name"`
	Description *string   `json:"description"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Permission represents a single module/sub_module/action flag.
type Permission struct {
	PermID    string    `json:"perm_id"`
	PackageID string    `json:"package_id"`
	Module    string    `json:"module"`
	SubModule string    `json:"sub_module"`
	Action    string    `json:"action"`
	IsAllowed bool      `json:"is_allowed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Repository handles access SQL operations.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) CreatePackage(ctx context.Context, code, displayName string, desc *string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO admin_svc.access_packages(package_code, display_name, description)
		 VALUES($1,$2,$3) RETURNING package_id::text`,
		code, displayName, desc).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("access.CreatePackage: %w", err)
	}
	return id, nil
}

func (r *Repository) GetPackageByID(ctx context.Context, id string) (*Package, error) {
	p := &Package{}
	err := r.pool.QueryRow(ctx,
		`SELECT package_id::text, package_code, display_name, description, is_active, created_at, updated_at
		 FROM admin_svc.access_packages WHERE package_id=$1`, id).
		Scan(&p.PackageID, &p.PackageCode, &p.DisplayName, &p.Description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (r *Repository) ListPackages(ctx context.Context) ([]*Package, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT package_id::text, package_code, display_name, description, is_active, created_at, updated_at
		 FROM admin_svc.access_packages ORDER BY package_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		p := &Package{}
		if err := rows.Scan(&p.PackageID, &p.PackageCode, &p.DisplayName, &p.Description,
			&p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) DeletePackage(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.access_packages SET is_active=false, updated_at=now() WHERE package_id=$1`, id)
	return err
}

// UpsertPermission inserts or updates a single permission flag.
func (r *Repository) UpsertPermission(ctx context.Context, p Permission) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO admin_svc.package_permissions(package_id,module,sub_module,action,is_allowed)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(package_id,module,sub_module,action)
		 DO UPDATE SET is_allowed=EXCLUDED.is_allowed, updated_at=now()`,
		p.PackageID, p.Module, p.SubModule, p.Action, p.IsAllowed)
	return err
}

// BulkUpsertPermissions upserts many permissions in a single transaction.
func (r *Repository) BulkUpsertPermissions(ctx context.Context, packageID string, perms []Permission) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, p := range perms {
		_, err := tx.Exec(ctx,
			`INSERT INTO admin_svc.package_permissions(package_id,module,sub_module,action,is_allowed)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(package_id,module,sub_module,action)
			 DO UPDATE SET is_allowed=EXCLUDED.is_allowed, updated_at=now()`,
			packageID, p.Module, p.SubModule, p.Action, p.IsAllowed)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetPermissions retrieves all permissions for a package, optionally filtered by module.
func (r *Repository) GetPermissions(ctx context.Context, packageID, module string) ([]*Permission, error) {
	var rows pgx.Rows
	var err error
	if module == "" {
		rows, err = r.pool.Query(ctx,
			`SELECT perm_id::text, package_id::text, module, sub_module, action, is_allowed, updated_at
			 FROM admin_svc.package_permissions WHERE package_id=$1`, packageID)
	} else {
		rows, err = r.pool.Query(ctx,
			`SELECT perm_id::text, package_id::text, module, sub_module, action, is_allowed, updated_at
			 FROM admin_svc.package_permissions WHERE package_id=$1 AND module=$2`, packageID, module)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Permission
	for rows.Next() {
		p := &Permission{}
		if err := rows.Scan(&p.PermID, &p.PackageID, &p.Module, &p.SubModule, &p.Action, &p.IsAllowed, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AssignPackage assigns a package to a deployment.
func (r *Repository) AssignPackage(ctx context.Context, deploymentID, packageID, assignedBy string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO admin_svc.deployment_packages(deployment_id, package_id, assigned_by)
		 VALUES($1,$2,$3) ON CONFLICT(deployment_id,package_id) DO NOTHING`,
		deploymentID, packageID, nullStr(assignedBy))
	return err
}

// CheckPermission performs the fast permission check used by both the API endpoint and the middleware.
func (r *Repository) CheckPermission(ctx context.Context, deploymentID, module, subModule, action string) (bool, string, error) {
	// 1. Check deployment is_active
	var isActive bool
	err := r.pool.QueryRow(ctx,
		`SELECT is_active FROM admin_svc.deployments WHERE deployment_id=$1`, deploymentID).Scan(&isActive)
	if err == pgx.ErrNoRows {
		return false, "deployment_not_found", nil
	}
	if err != nil {
		return false, "db_error", err
	}
	if !isActive {
		return false, "deployment_suspended", nil
	}

	// 2. Check active licence
	var licenceStatus string
	err = r.pool.QueryRow(ctx,
		`SELECT status FROM admin_svc.licences
		 WHERE deployment_id=$1 AND status IN ('ACTIVE','GRACE')
		 ORDER BY expires_at DESC LIMIT 1`, deploymentID).Scan(&licenceStatus)
	if err == pgx.ErrNoRows {
		return false, "licence_expired", nil
	}
	if err != nil {
		return false, "db_error", err
	}

	// 3. Get assigned package
	var packageID string
	err = r.pool.QueryRow(ctx,
		`SELECT package_id::text FROM admin_svc.deployment_packages WHERE deployment_id=$1 LIMIT 1`,
		deploymentID).Scan(&packageID)
	if err == pgx.ErrNoRows {
		return false, "no_package_assigned", nil
	}
	if err != nil {
		return false, "db_error", err
	}

	// 4. Lookup permission
	var allowed bool
	err = r.pool.QueryRow(ctx,
		`SELECT is_allowed FROM admin_svc.package_permissions
		 WHERE package_id=$1 AND module=$2 AND sub_module=$3 AND action=$4`,
		packageID, module, subModule, action).Scan(&allowed)
	if err == pgx.ErrNoRows {
		return false, "permission_not_found", nil
	}
	if err != nil {
		return false, "db_error", err
	}
	return allowed, "", nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
