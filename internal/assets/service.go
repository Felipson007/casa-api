package assets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// acquisitionDateTime converts a request's *civildate.Date into the *time.Time the domain struct
// still uses internally — nil-safe, since AcquisitionDate is optional on both Create and Update.
// Mirrors expenses/income's originalDateTime helper.
func acquisitionDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// defaultAttributes mirrors the assets.attributes column's DB default (NOT NULL DEFAULT '[]') —
// used whenever a request omits Attributes entirely, so Create/Update never send a Go nil slice
// (which the jsonb codec would encode as JSON null) where the column requires valid, non-null
// JSON.
var defaultAttributes = []AssetAttribute{}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, personID, categoryID *uuid.UUID, status *Status) ([]Asset, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Asset, error)
	Create(ctx context.Context, a Asset) error
	Update(ctx context.Context, a Asset) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns assets, optionally filtered by person, category, and/or status.
func (s *Service) List(ctx context.Context, personID, categoryID *uuid.UUID, status *Status) ([]AssetDTO, error) {
	list, err := s.repo.List(ctx, personID, categoryID, status)
	if err != nil {
		return nil, fmt.Errorf("assets: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single asset, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (AssetDTO, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AssetDTO{}, fmt.Errorf("assets: looking up: %w", err)
	}
	if a == nil {
		return AssetDTO{}, apperr.NotFound("asset not found")
	}
	return toDTO(*a), nil
}

// Create persists a new asset. Status always starts ACTIVE.
func (s *Service) Create(ctx context.Context, req CreateAssetRequest) (AssetDTO, error) {
	attributes := req.Attributes
	if attributes == nil {
		attributes = defaultAttributes
	}
	a := Asset{
		ID:                    uuid.New(),
		PersonID:              req.PersonID,
		Name:                  req.Name,
		CategoryID:            req.CategoryID,
		Identification:        req.Identification,
		AcquisitionValue:      req.AcquisitionValue,
		AcquisitionDate:       acquisitionDateTime(req.AcquisitionDate),
		CurrentEstimatedValue: req.CurrentEstimatedValue,
		Status:                StatusActive,
		HasDebt:               req.HasDebt,
		DebtAmount:            req.DebtAmount,
		Notes:                 req.Notes,
		Attributes:            attributes,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return AssetDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `a` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, a.ID)
	if err != nil {
		return AssetDTO{}, fmt.Errorf("assets: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces an asset's editable fields (including Status), or returns apperr.NotFound if it
// doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateAssetRequest) (AssetDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AssetDTO{}, fmt.Errorf("assets: looking up for update: %w", err)
	}
	if existing == nil {
		return AssetDTO{}, apperr.NotFound("asset not found")
	}

	attributes := req.Attributes
	if attributes == nil {
		attributes = defaultAttributes
	}
	updated := Asset{
		ID:                    id,
		PersonID:              req.PersonID,
		Name:                  req.Name,
		CategoryID:            req.CategoryID,
		Identification:        req.Identification,
		AcquisitionValue:      req.AcquisitionValue,
		AcquisitionDate:       acquisitionDateTime(req.AcquisitionDate),
		CurrentEstimatedValue: req.CurrentEstimatedValue,
		Status:                req.Status,
		HasDebt:               req.HasDebt,
		DebtAmount:            req.DebtAmount,
		Notes:                 req.Notes,
		Attributes:            attributes,
		CreatedAt:             existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return AssetDTO{}, err
	}
	// Re-read rather than mapping `updated` directly: money fields are echoed back at whatever
	// scale the caller sent them, not the numeric(15,2) canonical form Postgres actually stores —
	// same class of bug as F-3 (Hermes's HTTP endpoint sweep, msg 83), just on Update instead of
	// Create.
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AssetDTO{}, fmt.Errorf("assets: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes an asset by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("assets: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("asset not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("assets: deleting: %w", err)
	}
	return nil
}
