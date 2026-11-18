package pets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// birthDateTime converts a request's *civildate.Date into the *time.Time the domain struct still
// uses internally — nil-safe, since BirthDate is optional on both Create and Update. Mirrors
// expenses/income's originalDateTime helper.
func birthDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, activeOnly *bool) ([]Pet, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Pet, error)
	Create(ctx context.Context, p Pet) error
	Update(ctx context.Context, p Pet) error
	Delete(ctx context.Context, id uuid.UUID) error

	// The methods below back records.go's pet_records sub-resource (vaccines/weight/vet visits) —
	// see that file's doc comments.
	CreateRecord(ctx context.Context, r Record) (Record, error)
	ListRecordsByPet(ctx context.Context, petID uuid.UUID) ([]Record, error)
	ListUpcomingVaccines(ctx context.Context, petID uuid.UUID, today time.Time) ([]Record, error)
	FindRecordByID(ctx context.Context, id uuid.UUID) (*Record, error)
	DeleteRecord(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns pets, optionally filtered to only active ones (activeOnly == true) or only
// inactive ones (activeOnly == false); nil returns every pet.
func (s *Service) List(ctx context.Context, activeOnly *bool) ([]PetDTO, error) {
	list, err := s.repo.List(ctx, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("pets: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single pet, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (PetDTO, error) {
	p, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return PetDTO{}, fmt.Errorf("pets: looking up: %w", err)
	}
	if p == nil {
		return PetDTO{}, apperr.NotFound("pet not found")
	}
	return toDTO(*p), nil
}

// Create persists a new pet. Active always starts true.
func (s *Service) Create(ctx context.Context, req CreatePetRequest) (PetDTO, error) {
	p := Pet{
		ID:        uuid.New(),
		Name:      req.Name,
		Species:   req.Species,
		Breed:     req.Breed,
		BirthDate: birthDateTime(req.BirthDate),
		Notes:     req.Notes,
		Active:    true,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return PetDTO{}, fmt.Errorf("pets: creating: %w", err)
	}
	// Re-read rather than mapping the in-memory `p` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, p.ID)
	if err != nil {
		return PetDTO{}, fmt.Errorf("pets: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces a pet's editable fields (including Active), or returns apperr.NotFound if it
// doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdatePetRequest) (PetDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return PetDTO{}, fmt.Errorf("pets: looking up for update: %w", err)
	}
	if existing == nil {
		return PetDTO{}, apperr.NotFound("pet not found")
	}

	updated := Pet{
		ID:        id,
		Name:      req.Name,
		Species:   req.Species,
		Breed:     req.Breed,
		BirthDate: birthDateTime(req.BirthDate),
		Notes:     req.Notes,
		Active:    req.Active,
		CreatedAt: existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return PetDTO{}, fmt.Errorf("pets: updating: %w", err)
	}
	return toDTO(updated), nil
}

// Delete removes a pet by ID, or returns apperr.NotFound if it doesn't exist. Prefer
// Update(active=false) once a pet has documents/health records referencing it (ON DELETE SET
// NULL there — a hard delete just orphans those rows' pet_id, it doesn't fail).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("pets: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("pet not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("pets: deleting: %w", err)
	}
	return nil
}
