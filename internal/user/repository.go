package user

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is the domain model returned by the repository.
type User struct {
	UserID       string     `json:"user_id"`
	Username     string     `json:"username"`
	Email        string     `json:"email"`
	FullName     *string    `json:"full_name"`
	Phone        *string    `json:"phone"`
	Role         string     `json:"role"`
	Status       string     `json:"status"`
	CreatedBy    *string    `json:"created_by"`
	ApprovedBy   *string    `json:"approved_by"`
	ApprovedAt   *time.Time `json:"approved_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Repository handles all user SQL operations.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository returns a user Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, username, email, passwordHash, fullName, phone, role, createdBy string) (string, error) {
	var id string
	// createdBy must be a valid UUID for the uuid column; "MASTER" is not a UUID.
	// Pass nil (SQL NULL) when the actor is the master-key bootstrap user.
	var createdByVal any
	if createdBy != "" && createdBy != "MASTER" {
		createdByVal = createdBy
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO admin_svc.users(username,email,password_hash,full_name,phone,role,status,created_by)
		 VALUES($1,$2,$3,$4,$5,$6,'PENDING',$7)
		 RETURNING user_id::text`,
		username, email, passwordHash, nullStr(fullName), nullStr(phone), role, createdByVal,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("user.Create: %w", err)
	}
	return id, nil
}

func (r *Repository) GetByID(ctx context.Context, userID string) (*User, error) {
	u := &User{}
	err := r.pool.QueryRow(ctx,
		`SELECT user_id::text, username, email, full_name, phone, role, status,
		        created_by::text, approved_by::text, approved_at, created_at, updated_at
		 FROM admin_svc.users WHERE user_id=$1`, userID).
		Scan(&u.UserID, &u.Username, &u.Email, &u.FullName, &u.Phone, &u.Role, &u.Status,
			&u.CreatedBy, &u.ApprovedBy, &u.ApprovedAt, &u.CreatedAt, &u.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("user.GetByID: %w", err)
	}
	return u, nil
}

func (r *Repository) GetByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	var pwHash string
	err := r.pool.QueryRow(ctx,
		`SELECT user_id::text, username, email, password_hash, full_name, phone, role, status,
		        created_by::text, approved_by::text, approved_at, created_at, updated_at
		 FROM admin_svc.users WHERE username=$1`, username).
		Scan(&u.UserID, &u.Username, &u.Email, &pwHash, &u.FullName, &u.Phone, &u.Role, &u.Status,
			&u.CreatedBy, &u.ApprovedBy, &u.ApprovedAt, &u.CreatedAt, &u.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("user.GetByUsername: %w", err)
	}
	return u, nil
}

func (r *Repository) List(ctx context.Context, status string) ([]*User, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = r.pool.Query(ctx,
			`SELECT user_id::text, username, email, full_name, phone, role, status,
			        created_by::text, approved_by::text, approved_at, created_at, updated_at
			 FROM admin_svc.users ORDER BY created_at DESC`)
	} else {
		rows, err = r.pool.Query(ctx,
			`SELECT user_id::text, username, email, full_name, phone, role, status,
			        created_by::text, approved_by::text, approved_at, created_at, updated_at
			 FROM admin_svc.users WHERE status=$1 ORDER BY created_at DESC`, status)
	}
	if err != nil {
		return nil, fmt.Errorf("user.List: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.UserID, &u.Username, &u.Email, &u.FullName, &u.Phone, &u.Role, &u.Status,
			&u.CreatedBy, &u.ApprovedBy, &u.ApprovedAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *Repository) Approve(ctx context.Context, userID, approvedBy string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.users
		 SET status='APPROVED', approved_by=$2, approved_at=now(), updated_at=now()
		 WHERE user_id=$1`, userID, uuidVal(approvedBy))
	return err
}

func (r *Repository) Reject(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.users SET status='REJECTED', updated_at=now() WHERE user_id=$1`, userID)
	return err
}

func (r *Repository) Delete(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admin_svc.users SET status='DELETED', updated_at=now() WHERE user_id=$1`, userID)
	return err
}

// GetCheckers returns emails of all APPROVED CHECKER/MASTER users.
func (r *Repository) GetCheckers(ctx context.Context) ([]struct{ Email, Name, UserID string }, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT email, COALESCE(full_name,''), user_id::text
		 FROM admin_svc.users WHERE role IN ('CHECKER','MASTER') AND status='APPROVED'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ Email, Name, UserID string }
	for rows.Next() {
		var e struct{ Email, Name, UserID string }
		if err := rows.Scan(&e.Email, &e.Name, &e.UserID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// uuidVal returns nil when s is empty or the sentinel "MASTER" (not a valid UUID),
// preventing "invalid input syntax for type uuid" errors.
func uuidVal(s string) any {
	if s == "" || s == "MASTER" {
		return nil
	}
	return s
}
