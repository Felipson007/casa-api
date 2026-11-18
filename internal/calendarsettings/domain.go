// Package calendarsettings holds the household's single calendar sync destination: none (local
// only), iCloud, or Google. Only the selected provider runs its sync, so the two providers never
// compete over the same rows.
package calendarsettings

import (
	"context"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Target is the calendar_sync_settings.target CHECK.
type Target string

const (
	TargetNone   Target = "NONE"
	TargetICloud Target = "ICLOUD"
	TargetGoogle Target = "GOOGLE"
)

// Store is implemented by postgres.
type Store interface {
	Get(ctx context.Context) (Target, error)
	Set(ctx context.Context, t Target) error
}

// Repository is the persistence boundary consumed by the service.
type Repository = Store

type Service struct {
	repo Store
}

func NewService(repo Store) *Service {
	return &Service{repo: repo}
}

func (s *Service) Get(ctx context.Context) (Target, error) {
	return s.repo.Get(ctx)
}

func (s *Service) Set(ctx context.Context, t Target) error {
	switch t {
	case TargetNone, TargetICloud, TargetGoogle:
	default:
		return apperr.Validation("target must be NONE, ICLOUD or GOOGLE", "target")
	}
	return s.repo.Set(ctx, t)
}
