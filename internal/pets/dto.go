package pets

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// PetDTO is the JSON shape returned to clients. BirthDate is civildate.Date, not time.Time — see
// its doc comment (a calendar date has no time-of-day/zone to misinterpret in local formatting).
type PetDTO struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Species   Species         `json:"species"`
	Breed     *string         `json:"breed,omitempty"`
	BirthDate *civildate.Date `json:"birthDate,omitempty"`
	Notes     *string         `json:"notes,omitempty"`
	Active    bool            `json:"active"`
	CreatedAt time.Time       `json:"createdAt"`
}

// CreatePetRequest is the JSON body accepted by POST /api/pets. Active always starts true —
// mirrors the DB column default.
type CreatePetRequest struct {
	Name      string          `json:"name" validate:"required"`
	Species   Species         `json:"species" validate:"required,oneof=DOG CAT BIRD FISH OTHER"`
	Breed     *string         `json:"breed"`
	BirthDate *civildate.Date `json:"birthDate"`
	Notes     *string         `json:"notes"`
}

// UpdatePetRequest is the JSON body accepted by PUT /api/pets/{id}. Active is settable here —
// same reasoning as clients.UpdateClientRequest (deactivate rather than hard-delete once a pet
// has history, e.g. documents/health_records referencing it).
type UpdatePetRequest struct {
	Name      string          `json:"name" validate:"required"`
	Species   Species         `json:"species" validate:"required,oneof=DOG CAT BIRD FISH OTHER"`
	Breed     *string         `json:"breed"`
	BirthDate *civildate.Date `json:"birthDate"`
	Notes     *string         `json:"notes"`
	Active    bool            `json:"active"`
}
