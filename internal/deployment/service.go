package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"CimplrCorpSaas/admin/internal/notification"
)

// Service contains deployment business logic.
type Service struct {
	repo *Repository
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{repo: NewRepository(pool), pool: pool}
}

func (s *Service) Create(ctx context.Context, actorID, actorRole string, d *Deployment) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, err := s.repo.Create(ctx2, d, actorID)
	if err != nil {
		return "", err
	}
	s.audit(ctx2, "DEPLOYMENT", id, "CREATE", actorID, actorRole, nil, map[string]any{"company": d.CompanyName})
	return id, nil
}

func (s *Service) Approve(ctx context.Context, actorID, actorRole, deploymentID string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	d, err := s.repo.GetByID(ctx2, deploymentID)
	if err != nil || d == nil {
		return fmt.Errorf("deployment not found")
	}
	if err := s.repo.Approve(ctx2, deploymentID, actorID); err != nil {
		return err
	}
	s.audit(ctx2, "DEPLOYMENT", deploymentID, "APPROVE", actorID, actorRole, nil, nil)
	_ = notification.Enqueue(ctx2, s.pool, notification.EnqueueRequest{
		EventID:        "DEPLOYMENT_APPROVED",
		RecipientEmail: d.CompanyEmail,
		RecipientName:  d.CompanyName,
		TemplateData:   map[string]string{"CompanyName": d.CompanyName},
	})
	return nil
}

func (s *Service) Reject(ctx context.Context, actorID, actorRole, deploymentID, reason string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.repo.Reject(ctx2, deploymentID); err != nil {
		return err
	}
	s.audit(ctx2, "DEPLOYMENT", deploymentID, "REJECT", actorID, actorRole, nil, map[string]any{"reason": reason})
	return nil
}

func (s *Service) Delete(ctx context.Context, actorID, actorRole, deploymentID string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.repo.Delete(ctx2, deploymentID); err != nil {
		return err
	}
	s.audit(ctx2, "DEPLOYMENT", deploymentID, "DELETE", actorID, actorRole, nil, nil)
	return nil
}

func (s *Service) Get(ctx context.Context, id string) (*Deployment, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.GetByID(ctx2, id)
}

func (s *Service) List(ctx context.Context, status string) ([]*Deployment, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.List(ctx2, status)
}

func (s *Service) audit(ctx context.Context, entityType, entityID, action, actorID, actorRole string, old, new_ any) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO admin_svc.audit_log
		 (entity_type,entity_id,action,actor_user_id,actor_role,old_value,new_value)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		entityType, entityID, action, uuidVal(actorID), actorRole, old, new_)
}
