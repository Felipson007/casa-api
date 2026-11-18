// Package pets is the household-pets module (pets in the old app too) — owned by Claude per
// decisions/DECISIONS.md D-005. Mirrors internal/clients' shape (closest sibling: a simple
// standalone entity with an "active" flag, no FKs of its own).
package pets

import (
	"time"

	"github.com/google/uuid"
)

// Species mirrors the pets.species CHECK constraint.
type Species string

const (
	SpeciesDog   Species = "DOG"
	SpeciesCat   Species = "CAT"
	SpeciesBird  Species = "BIRD"
	SpeciesFish  Species = "FISH"
	SpeciesOther Species = "OTHER"
)

// Pet mirrors the pets table (internal/migrations/000001_init_schema.up.sql). It has no FKs of
// its own — documents/health_records/pet_records reference IT, not the other way around.
type Pet struct {
	ID        uuid.UUID
	Name      string
	Species   Species
	Breed     *string
	BirthDate *time.Time
	Notes     *string
	Active    bool
	CreatedAt time.Time
}
