package settings

import "context"

// Service is the settings business logic. The singleton has no create/delete — Get/Update only.
type Service struct {
	repo Repository
}

// NewService builds a settings service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Get(ctx context.Context) (*Settings, error) {
	return s.repo.Get(ctx)
}

func (s *Service) Update(ctx context.Context, req UpdateSettingsRequest) (*Settings, error) {
	existing, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	existing.CompanyName = req.CompanyName
	existing.PixKey = req.PixKey
	existing.LogoPath = req.LogoPath
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	// Reload so UpdatedAt reflects the DB write (F-3 pattern: re-read after write).
	return s.repo.Get(ctx)
}
