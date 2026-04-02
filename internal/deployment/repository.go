package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deployment is the domain model. db_password is never returned in plain form.
type Deployment struct {
	DeploymentID   string     `json:"deployment_id"`
	CompanyName    string     `json:"company_name"`
	CompanyEmail   string     `json:"company_email"`
	CompanyPhone   *string    `json:"company_phone"`
	ContactPerson  *string    `json:"contact_person"`
	CompanyAddress *string    `json:"company_address"`
	DBUser         string     `json:"db_user"`
	DBPassword     string     `json:"db_password"` // always "***" outside repo
	DBHost         string     `json:"db_host"`
	DBPort         string     `json:"db_port"`
	DBName         string     `json:"db_name"`
	DBURL          *string    `json:"db_url"`
	Status         string     `json:"status"`
	IsActive       bool       `json:"is_active"`
	CreatedBy      *string    `json:"created_by"`
	ApprovedBy     *string    `json:"approved_by"`
	ApprovedAt     *time.Time `json:"approved_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Repository handles deployment SQL operations.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, d *Deployment, createdBy string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO admin_svc.deployments
		 (company_name,company_email,company_phone,contact_person,company_address,
		  db_user,db_password,db_host,db_port,db_name,db_url,status,is_active,created_by)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'PENDING',false,$12)
		 RETURNING deployment_id::text`,
		d.CompanyName, d.CompanyEmail, d.CompanyPhone, d.ContactPerson, d.CompanyAddress,
		d.DBUser, d.DBPassword, d.DBHost, d.DBPort, d.DBName, d.DBURL, uuidVal(createdBy),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("deployment.Create: %w", err)
	}
	return id, nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Deployment, error) {
	d := &Deployment{}
	err := r.pool.QueryRow(ctx,
		`SELECT deployment_id::text, company_name, company_email, company_phone,
		        contact_person, company_address, db_user, db_password, db_host, db_port,
		        db_name, db_url, status, is_active, created_by::text, approved_by::text,
		        approved_at, created_at, updated_at
		 FROM admin_svc.deployments WHERE deployment_id=$1`, id).
		Scan(&d.DeploymentID, &d.CompanyName, &d.CompanyEmail, &d.CompanyPhone,
			&d.ContactPerson, &d.CompanyAddress, &d.DBUser, &d.DBPassword, &d.DBHost, &d.DBPort,
			&d.DBName, &d.DBURL, &d.Status, &d.IsActive, &d.CreatedBy, &d.ApprovedBy,
			&d.ApprovedAt, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deployment.GetByID: %w", err)
	}
	d.DBPassword = "***"
	return d, nil
}

func (r *Repository) List(ctx context.Context, status string) ([]*Deployment, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = r.pool.Query(ctx,
			`SELECT deployment_id::text, company_name, company_email, company_phone,
			        contact_person, company_address, db_user, db_password, db_host, db_port,
			        db_name, db_url, status, is_active, created_by::text, approved_by::text,
			        approved_at, created_at, updated_at
			 FROM admin_svc.deployments ORDER BY created_at DESC`)
	} else {
		rows, err = r.pool.Query(ctx,
			`SELECT deployment_id::text, company_name, company_email, company_phone,
			        contact_person, company_address, db_user, db_password, db_host, db_port,
			        db_name, db_url, status, is_active, created_by::text, approved_by::text,
			        approved_at, created_at, updated_at
			 FROM admin_svc.deployments WHERE status=$1 ORDER BY created_at DESC`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Deployment
	for rows.Next() {
		d := &Deployment{}
		if err := rows.Scan(&d.DeploymentID, &d.CompanyName, &d.CompanyEmail, &d.CompanyPhone,
			&d.ContactPerson, &d.CompanyAddress, &d.DBUser, &d.DBPassword, &d.DBHost, &d.DBPort,
			&d.DBName, &d.DBURL, &d.Status, &d.IsActive, &d.CreatedBy, &d.ApprovedBy,
			&d.ApprovedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.DBPassword = "***"
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) Approve(ctx context.Context, id, approvedBy string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.deployments
		 SET status='APPROVED', is_active=true, approved_by=$2, approved_at=now(), updated_at=now()
		 WHERE deployment_id=$1`, id, uuidVal(approvedBy))
	return err
}

func (r *Repository) Reject(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.deployments SET status='REJECTED', is_active=false, updated_at=now()
		 WHERE deployment_id=$1`, id)
	return err
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.deployments SET status='DELETED', is_active=false, updated_at=now()
		 WHERE deployment_id=$1`, id)
	return err
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
