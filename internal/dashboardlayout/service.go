package dashboardlayout

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Get returns the given app user's saved layout. A user who has never saved one gets `layout: null`
// rather than an error — the handler answers 200 and the frontend treats null as "never customized",
// falling back to its built-in default (same as the old 404 it used to handle).
func (s *Service) Get(ctx context.Context, appUserID uuid.UUID) (LayoutDTO, error) {
	l, err := s.repo.Get(ctx, appUserID)
	if err != nil {
		return LayoutDTO{}, err
	}
	return toDTO(l), nil
}

// Set validates and persists the given app user's layout. The payload is opaque to this module
// (see package doc) — the only rule enforced here is "well-formed JSON array", so a malformed or
// wrong-shaped body from a future non-RGL client fails fast instead of being stored as garbage.
func (s *Service) Set(ctx context.Context, appUserID uuid.UUID, req SetLayoutRequest) (LayoutDTO, error) {
	items := bytes.TrimSpace(req.Layout)
	if len(items) == 0 || !json.Valid(items) {
		return LayoutDTO{}, apperr.Validation("layout must be well-formed JSON", "layout")
	}
	if items[0] != '[' {
		return LayoutDTO{}, apperr.Validation("layout must be a JSON array", "layout")
	}

	l, err := s.repo.Set(ctx, appUserID, items)
	if err != nil {
		return LayoutDTO{}, err // already an *apperr.Error or a wrapped DB error, per chat.Service.Create
	}
	return toDTO(l), nil
}
