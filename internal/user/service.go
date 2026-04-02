package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"CimplrCorpSaas/admin/internal/notification"
)

// ErrNotFound is returned when a user is not found.
var ErrNotFound = errors.New("user not found")

// ErrForbidden is returned on role/ownership violations.
var ErrForbidden = errors.New("forbidden")

// Service contains business logic for user management.
type Service struct {
	repo *Repository
	pool *pgxpool.Pool
}

// NewService returns a user Service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{repo: NewRepository(pool), pool: pool}
}

// CreateUser hashes the password and inserts a new PENDING user.
func (s *Service) CreateUser(ctx context.Context, actorID, actorRole,
	username, email, fullName, phone, role, password string) (string, error) {

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	userID, err := s.repo.Create(ctx2, username, email, string(hash), fullName, phone, role, actorID)
	if err != nil {
		return "", err
	}

	// Audit
	s.writeAudit(ctx2, "USER", userID, "CREATE", actorID, actorRole, nil, map[string]any{"username": username, "role": role})

	// Notify all checkers
	checkers, _ := s.repo.GetCheckers(ctx2)
	for _, c := range checkers {
		_ = notification.Enqueue(ctx2, s.pool, notification.EnqueueRequest{
			EventID:        "USER_CREATED",
			RecipientEmail: c.Email,
			RecipientName:  c.Name,
			RecipientUserID: c.UserID,
			TemplateData:   map[string]string{"Username": username, "Role": role},
		})
	}

	return userID, nil
}

// ApproveUser sets a user to APPROVED.
func (s *Service) ApproveUser(ctx context.Context, actorID, actorRole, userID string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	u, err := s.repo.GetByID(ctx2, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrNotFound
	}
	if u.CreatedBy != nil && *u.CreatedBy == actorID {
		return fmt.Errorf("%w: cannot approve your own record", ErrForbidden)
	}
	if u.UserID == actorID {
		return fmt.Errorf("%w: cannot approve yourself", ErrForbidden)
	}

	if err := s.repo.Approve(ctx2, userID, actorID); err != nil {
		return err
	}
	s.writeAudit(ctx2, "USER", userID, "APPROVE", actorID, actorRole, nil, nil)

	// Notify user
	_ = notification.Enqueue(ctx2, s.pool, notification.EnqueueRequest{
		EventID:        "USER_APPROVED",
		RecipientEmail: u.Email,
		RecipientName:  strVal(u.FullName),
		RecipientUserID: u.UserID,
		TemplateData:   map[string]string{"Name": strVal(u.FullName)},
	})
	return nil
}

// RejectUser sets a user to REJECTED.
func (s *Service) RejectUser(ctx context.Context, actorID, actorRole, userID, reason string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	u, err := s.repo.GetByID(ctx2, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrNotFound
	}
	if err := s.repo.Reject(ctx2, userID); err != nil {
		return err
	}
	s.writeAudit(ctx2, "USER", userID, "REJECT", actorID, actorRole, nil, map[string]any{"reason": reason})

	_ = notification.Enqueue(ctx2, s.pool, notification.EnqueueRequest{
		EventID:        "USER_REJECTED",
		RecipientEmail: u.Email,
		RecipientName:  strVal(u.FullName),
		RecipientUserID: u.UserID,
		TemplateData:   map[string]string{"Name": strVal(u.FullName), "Reason": reason},
	})
	return nil
}

// DeleteUser soft-deletes a user.
func (s *Service) DeleteUser(ctx context.Context, actorID, actorRole, userID string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.repo.Delete(ctx2, userID); err != nil {
		return err
	}
	s.writeAudit(ctx2, "USER", userID, "DELETE", actorID, actorRole, nil, nil)
	return nil
}

// GetUser retrieves one user by ID.
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.GetByID(ctx2, userID)
}

// ListUsers retrieves users optionally filtered by status.
func (s *Service) ListUsers(ctx context.Context, status string) ([]*User, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.List(ctx2, status)
}

func (s *Service) writeAudit(ctx context.Context, entityType, entityID, action, actorID, actorRole string, old, new_ any) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO admin_svc.audit_log
		 (entity_type,entity_id,action,actor_user_id,actor_role,old_value,new_value)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		entityType, entityID, action, uuidVal(actorID), actorRole,
		jsonbVal(old), jsonbVal(new_))
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func jsonbVal(v any) any {
	if v == nil {
		return nil
	}
	return v
}
