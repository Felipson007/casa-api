package people

import (
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/google/uuid"
)

// CreatePersonRequest carries the fields for creating a person. Active always starts true (mirrors
// the DB column default) — not part of the create request, same idiom as clients.CreateClientRequest.
// SelfKinship is settable only here, never on UpdatePersonRequest (see that struct's doc comment
// and Service.UpdatePerson) — a newly-created person has no legacy snapshot to protect, so there is
// nothing wrong with the caller supplying an initial value (most commonly SELF, for "this is me"),
// but once set it follows the same read-only-after-create rule as every other legacy field.
type CreatePersonRequest struct {
	Name        string                 `json:"name" validate:"required"`
	Nickname    *string                `json:"nickname"`
	BirthDate   *civildate.Date        `json:"birthDate"`
	Gender      personrelations.Gender `json:"gender" validate:"omitempty,oneof=M F"`
	Notes       *string                `json:"notes"`
	Email       *string                `json:"email" validate:"omitempty,email"`
	SelfKinship *LegacyKinship         `json:"selfKinship" validate:"omitempty,oneof=SELF FATHER MOTHER BROTHER SISTER SON DAUGHTER GRANDPARENT GRANDSON GRANDDAUGHTER UNCLE AUNT NEPHEW NIECE FATHER_IN_LAW MOTHER_IN_LAW SON_IN_LAW DAUGHTER_IN_LAW SPOUSE OTHER"`
}

// UpdatePersonRequest carries the fields for PUT /api/people/{id} — Active is settable here
// (deactivating rather than deleting is the expected way to retire a person still referenced by
// historical expenses/assets/documents), mirroring clients.UpdateClientRequest.
type UpdatePersonRequest struct {
	Name      string                 `json:"name" validate:"required"`
	Nickname  *string                `json:"nickname"`
	BirthDate *civildate.Date        `json:"birthDate"`
	Gender    personrelations.Gender `json:"gender" validate:"omitempty,oneof=M F"`
	Notes     *string                `json:"notes"`
	Active    bool                   `json:"active"`
	Email     *string                `json:"email" validate:"omitempty,email"`
}

// CreateRelationshipRequest carries the fields for creating a primary edge.
type CreateRelationshipRequest struct {
	FromPersonID uuid.UUID               `json:"fromPersonId" validate:"required"`
	ToPersonID   uuid.UUID               `json:"toPersonId" validate:"required"`
	Type         personrelations.RelType `json:"type" validate:"required,oneof=MOTHER_OF FATHER_OF SPOUSE_OF"`
}

func toRelationshipDTO(r RelationshipDTO) RelationshipDTO {
	return r
}
