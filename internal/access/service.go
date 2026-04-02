package access

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service contains access/package business logic.
type Service struct {
	repo *Repository
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{repo: NewRepository(pool), pool: pool}
}

func (s *Service) CreatePackage(ctx context.Context, code, displayName string, desc *string) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.CreatePackage(ctx2, code, displayName, desc)
}

func (s *Service) GetPackage(ctx context.Context, id string) (*Package, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.GetPackageByID(ctx2, id)
}

func (s *Service) ListPackages(ctx context.Context) ([]*Package, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.ListPackages(ctx2)
}

func (s *Service) DeletePackage(ctx context.Context, id string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.DeletePackage(ctx2, id)
}

func (s *Service) SetPermission(ctx context.Context, p Permission) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.UpsertPermission(ctx2, p)
}

func (s *Service) BulkSetPermissions(ctx context.Context, packageID string, perms []Permission) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.BulkUpsertPermissions(ctx2, packageID, perms)
}

func (s *Service) GetPermissions(ctx context.Context, packageID, module string) ([]*Permission, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.GetPermissions(ctx2, packageID, module)
}

func (s *Service) AssignPackage(ctx context.Context, deploymentID, packageID, assignedBy string) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.AssignPackage(ctx2, deploymentID, packageID, assignedBy)
}

func (s *Service) CheckPermission(ctx context.Context, deploymentID, module, subModule, action string) (bool, string, error) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repo.CheckPermission(ctx2, deploymentID, module, subModule, action)
}
