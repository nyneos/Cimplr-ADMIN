package licence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service contains licence business logic.
type Service struct {
	repo *Repository
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{repo: NewRepository(pool), pool: pool}
}

func (s *Service) Create(ctx context.Context, actorID, deploymentID string, startsAt, expiresAt time.Time, graceDays int) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, err := s.repo.Create(ctx2, deploymentID, startsAt, expiresAt, graceDays, actorID)
	if err != nil {
		return "", err
	}
	s.audit(ctx2, "LICENCE", id, "CREATE", actorID, nil, map[string]any{
		"deployment_id": deploymentID, "expires_at": expiresAt,
	})
	return id, nil
}

func (s *Service) Renew(ctx context.Context, actorID, licenceID string, newExpiresAt time.Time) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.repo.Renew(ctx2, licenceID, newExpiresAt, actorID); err != nil {
		return fmt.Errorf("licence.Renew: %w", err)
	}
	s.audit(ctx2, "LICENCE", licenceID, "RENEW", actorID, nil, map[string]any{"new_expires_at": newExpiresAt})
	return nil
}

func (s *Service) GetByDeployment(ctx context.Context, deploymentID string) ([]*Licence, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.GetByDeployment(ctx2, deploymentID)
}

func (s *Service) List(ctx context.Context, status string) ([]*Licence, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.List(ctx2, status)
}

func (s *Service) audit(ctx context.Context, entityType, entityID, action, actorID string, old, new_ any) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO admin_svc.audit_log(entity_type,entity_id,action,actor_user_id,old_value,new_value)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		entityType, entityID, action, uuidVal(actorID), old, new_)
}
