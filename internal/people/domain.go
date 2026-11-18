// Package people is the reference module for the family graph (people + person_relationships),
// ported after categories to validate the layering with a graph-shaped domain. It pairs with
// internal/personrelations, which derives indirect kinship from the primary edges.
// See docs/architecture-plan.md section 10, item 4/8.
package people

import (
	"time"

	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/google/uuid"
)

// Person mirrors the pessoas/domain.Pessoa entity, renamed per section 9. The relationship to
// "self" is primarily derived from person_relationships by the personrelations package; SelfKinship
// (migration 000005) is a read-mostly fallback carrying the old value across for people the graph
// can't (yet) reach — see its own doc comment. "Gender" replaces the multi-signal heuristic that
// used to partly read the old parentesco column. Nickname/BirthDate/Notes/Active/Email were
// restored in migration 000004 — 000001 originally dropped them without a replacement (found while
// wiring the real frontend).
type Person struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Nickname  *string                `json:"nickname,omitempty"`
	BirthDate *civildate.Date        `json:"birthDate,omitempty"` // civildate: see its doc comment — a calendar date, not an instant
	Gender    personrelations.Gender `json:"gender,omitempty"`
	Notes     *string                `json:"notes,omitempty"`
	Active    bool                   `json:"active"`
	Email     *string                `json:"email,omitempty"`
	// SelfKinship is the legacy "relationship to the app owner" value carried across verbatim from
	// the old Java Pessoa.parentesco — see migration 000005's doc comment for why this coexists
	// with (not replaces) the person_relationships graph as a production-parity display fallback.
	SelfKinship *LegacyKinship `json:"selfKinship,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// LegacyKinship is the closed enum matching migration 000005's CHECK constraint — a straight
// English translation of the old Java Parentesco enum, gendered exactly as that one was
// (FATHER/MOTHER, SON/DAUGHTER, ... unlike personrelations.Kinship, which deliberately left some
// terms gender-neutral since it's meant to be combined with the target's own Gender field instead).
type LegacyKinship string

const (
	LegacyKinshipSelf          LegacyKinship = "SELF"
	LegacyKinshipFather        LegacyKinship = "FATHER"
	LegacyKinshipMother        LegacyKinship = "MOTHER"
	LegacyKinshipBrother       LegacyKinship = "BROTHER"
	LegacyKinshipSister        LegacyKinship = "SISTER"
	LegacyKinshipSon           LegacyKinship = "SON"
	LegacyKinshipDaughter      LegacyKinship = "DAUGHTER"
	LegacyKinshipGrandparent   LegacyKinship = "GRANDPARENT"
	LegacyKinshipGrandson      LegacyKinship = "GRANDSON"
	LegacyKinshipGranddaughter LegacyKinship = "GRANDDAUGHTER"
	LegacyKinshipUncle         LegacyKinship = "UNCLE"
	LegacyKinshipAunt          LegacyKinship = "AUNT"
	LegacyKinshipNephew        LegacyKinship = "NEPHEW"
	LegacyKinshipNiece         LegacyKinship = "NIECE"
	LegacyKinshipFatherInLaw   LegacyKinship = "FATHER_IN_LAW"
	LegacyKinshipMotherInLaw   LegacyKinship = "MOTHER_IN_LAW"
	LegacyKinshipSonInLaw      LegacyKinship = "SON_IN_LAW"
	LegacyKinshipDaughterInLaw LegacyKinship = "DAUGHTER_IN_LAW"
	LegacyKinshipSpouse        LegacyKinship = "SPOUSE"
	LegacyKinshipOther         LegacyKinship = "OTHER"
)

// Relationship mirrors pessoa_relacoes: one primary edge. Only MOTHER_OF / FATHER_OF / SPOUSE_OF
// are stored; everything else is derived (section 10, item 4).
type Relationship struct {
	ID           uuid.UUID
	FromPersonID uuid.UUID
	ToPersonID   uuid.UUID
	Type         personrelations.RelType
	CreatedAt    time.Time
}

// RelationshipDTO is the API view of a primary edge, including the resolved names from the view
// v_person_relationships.
type RelationshipDTO struct {
	ID           uuid.UUID               `json:"id"`
	FromPersonID uuid.UUID               `json:"fromPersonId"`
	FromName     string                  `json:"fromName"`
	Type         personrelations.RelType `json:"type"`
	ToPersonID   uuid.UUID               `json:"toPersonId"`
	ToName       string                  `json:"toName"`
}

// ErrRelationshipExists is returned when a relationship edge duplicates an existing one
// (unique constraint idx_person_relationships_sem_duplicata).
var ErrRelationshipExists = &relationshipError{"relationship already exists"}

// ErrPersonNotFound is returned by UpdatePerson/DeletePerson when no row matched — defensive
// against a delete-after-check race; the common "was never there" case is already caught earlier
// by Service's own FindPerson check.
var ErrPersonNotFound = &relationshipError{"person not found"}

type relationshipError struct{ msg string }

func (e *relationshipError) Error() string { return e.msg }

// DerivedRelationshipDTO wraps a derived kinship term for a person relative to an origin.
type DerivedRelationshipDTO struct {
	PersonID    uuid.UUID               `json:"personId"`
	Parentesco  personrelations.Kinship `json:"kinship"`
	RotuloLivre string                  `json:"label"`
}

// GenderOf converts a DB string into a Gender. Empty/invalid -> GenderUnknown.
func GenderOf(s string) personrelations.Gender {
	switch s {
	case "M":
		return personrelations.GenderMale
	case "F":
		return personrelations.GenderFemale
	default:
		return personrelations.GenderUnknown
	}
}
