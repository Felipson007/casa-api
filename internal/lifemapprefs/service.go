package lifemapprefs

import (
	"context"

	"github.com/google/uuid"
)

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Get returns the given app user's saved preferences. A user who has never saved any gets an empty
// set (no hidden areas) rather than an error — the handler answers 200 with `hiddenAreas: []`, and
// the frontend treats that as "nothing hidden" (same as the old 404 it used to handle).
func (s *Service) Get(ctx context.Context, appUserID uuid.UUID) (PreferencesDTO, error) {
	p, err := s.repo.Get(ctx, appUserID)
	if err != nil {
		return PreferencesDTO{}, err
	}
	return toDTO(p), nil
}

// Set persists the given app user's hidden area ids. No validation against a known set of area
// ids on purpose — see package doc: unknown ids are harmless, the frontend just ignores them.
func (s *Service) Set(ctx context.Context, appUserID uuid.UUID, req SetPreferencesRequest) (PreferencesDTO, error) {
	hidden := req.HiddenAreas
	if hidden == nil {
		hidden = []string{}
	}
	p, err := s.repo.Set(ctx, appUserID, hidden)
	if err != nil {
		return PreferencesDTO{}, err // already an *apperr.Error or a wrapped DB error, per chat.Service.Create
	}
	return toDTO(p), nil
}
