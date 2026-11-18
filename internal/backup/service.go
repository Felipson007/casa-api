package backup

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Service is the backup config business logic.
type Service struct {
	repo Repository
}

// NewService builds a backup config service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]BackupConfig, error) {
	return s.repo.List(ctx)
}

func (s *Service) Create(ctx context.Context, req CreateBackupConfigRequest) (*BackupConfig, error) {
	c := &BackupConfig{
		ID:     uuid.NewString(),
		Hour:   req.Hour,
		Minute: req.Minute,
	}
	return s.repo.Create(ctx, c)
}

func (s *Service) Get(ctx context.Context, id string) (*BackupConfig, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("backup configuration not found")
		}
		return nil, err
	}
	return c, nil
}

func (s *Service) Update(ctx context.Context, id string, req UpdateBackupConfigRequest) (*BackupConfig, error) {
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("backup configuration not found")
		}
		return nil, err
	}
	existing.Hour = req.Hour
	existing.Minute = req.Minute
	return s.repo.Update(ctx, existing)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("backup configuration not found")
		}
		return err
	}
	return nil
}
