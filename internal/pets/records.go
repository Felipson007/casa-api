package pets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// records.go ports RegistroPetService (pet_records — vaccines/weight/vet visits) — read from the
// real Java rather than guessed. The pet_records TABLE has existed since migration 000001; no Go
// module was ever built against it (found missing while wiring the real frontend).

// RecordStatus mirrors the pet_records.status CHECK constraint.
type RecordStatus string

const (
	RecordStatusDone      RecordStatus = "DONE"
	RecordStatusPending   RecordStatus = "PENDING"
	RecordStatusCancelled RecordStatus = "CANCELLED"
)

// Record mirrors the pet_records table.
type Record struct {
	ID         uuid.UUID
	PetID      uuid.UUID
	Type       string // free-form in the DB (varchar(30), no CHECK) — VACCINE/WEIGHT/VET_VISIT/DEWORMING/NOTE by convention
	Name       *string
	RecordDate time.Time
	Status     RecordStatus
	Amount     *string
	Notes      *string
	CreatedAt  time.Time
}

// RecordDTO is the JSON shape returned to clients. RecordDate is civildate.Date, not time.Time —
// see its doc comment.
type RecordDTO struct {
	ID         uuid.UUID      `json:"id"`
	PetID      uuid.UUID      `json:"petId"`
	Type       string         `json:"type"`
	Name       *string        `json:"name,omitempty"`
	RecordDate civildate.Date `json:"recordDate"`
	Status     RecordStatus   `json:"status"`
	Amount     *string        `json:"amount,omitempty"`
	Notes      *string        `json:"notes,omitempty"`
}

// CreateRecordRequest is the JSON body accepted by POST /api/pets/{petId}/records. Status defaults
// to DONE when omitted — mirrors RegistroPetService.criar exactly
// ("request.status() != null ? request.status() : StatusRegistroPet.REALIZADO").
type CreateRecordRequest struct {
	Type       string         `json:"type" validate:"required"`
	Name       *string        `json:"name"`
	RecordDate civildate.Date `json:"recordDate" validate:"required"`
	Status     *RecordStatus  `json:"status"`
	Amount     *string        `json:"amount"`
	Notes      *string        `json:"notes"`
}

// CreateRecord persists a new pet record, verifying the pet exists first — mirrors
// RegistroPetService.criar.
func (s *Service) CreateRecord(ctx context.Context, petID uuid.UUID, req CreateRecordRequest) (RecordDTO, error) {
	pet, err := s.repo.FindByID(ctx, petID)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("pets: looking up pet for record: %w", err)
	}
	if pet == nil {
		return RecordDTO{}, apperr.NotFound("pet not found")
	}
	status := RecordStatusDone
	if req.Status != nil {
		status = *req.Status
	}
	r := Record{
		ID: uuid.New(), PetID: petID, Type: req.Type, Name: req.Name, RecordDate: req.RecordDate.Time(),
		Status: status, Amount: req.Amount, Notes: req.Notes,
	}
	created, err := s.repo.CreateRecord(ctx, r)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("pets: creating record: %w", err)
	}
	return toRecordDTO(created), nil
}

// ListRecords returns a pet's records, most recent first, or apperr.NotFound if the pet itself
// doesn't exist.
func (s *Service) ListRecords(ctx context.Context, petID uuid.UUID) ([]RecordDTO, error) {
	pet, err := s.repo.FindByID(ctx, petID)
	if err != nil {
		return nil, fmt.Errorf("pets: looking up pet for records: %w", err)
	}
	if pet == nil {
		return nil, apperr.NotFound("pet not found")
	}
	list, err := s.repo.ListRecordsByPet(ctx, petID)
	if err != nil {
		return nil, fmt.Errorf("pets: listing records: %w", err)
	}
	return toRecordDTOs(list), nil
}

// ListUpcomingVaccines returns a pet's scheduled-but-not-yet-given vaccines.
func (s *Service) ListUpcomingVaccines(ctx context.Context, petID uuid.UUID, now time.Time) ([]RecordDTO, error) {
	pet, err := s.repo.FindByID(ctx, petID)
	if err != nil {
		return nil, fmt.Errorf("pets: looking up pet for upcoming vaccines: %w", err)
	}
	if pet == nil {
		return nil, apperr.NotFound("pet not found")
	}
	list, err := s.repo.ListUpcomingVaccines(ctx, petID, now)
	if err != nil {
		return nil, fmt.Errorf("pets: listing upcoming vaccines: %w", err)
	}
	return toRecordDTOs(list), nil
}

// DeleteRecord removes a pet record outright — mirrors RegistroPetService.remover (no soft-delete
// concept for records, unlike the pet itself).
func (s *Service) DeleteRecord(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindRecordByID(ctx, id)
	if err != nil {
		return fmt.Errorf("pets: looking up record: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("record not found")
	}
	if err := s.repo.DeleteRecord(ctx, id); err != nil {
		return fmt.Errorf("pets: deleting record: %w", err)
	}
	return nil
}

func toRecordDTO(r Record) RecordDTO {
	return RecordDTO{
		ID: r.ID, PetID: r.PetID, Type: r.Type, Name: r.Name, RecordDate: civildate.Of(r.RecordDate),
		Status: r.Status, Amount: r.Amount, Notes: r.Notes,
	}
}

func toRecordDTOs(list []Record) []RecordDTO {
	dtos := make([]RecordDTO, len(list))
	for i, r := range list {
		dtos[i] = toRecordDTO(r)
	}
	return dtos
}
