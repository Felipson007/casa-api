package people

import (
	"context"

	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/google/uuid"
)

// Repository is defined at the point of consumption (Go idiom, section 1). The postgres subpackage
// implements it structurally; nothing in this package imports postgres.
type Repository interface {
	// ListPeople returns every person, ordered by name. query, when non-empty, filters to people
	// whose name/nickname/email match it case-insensitively.
	ListPeople(ctx context.Context, query string) ([]Person, error)
	FindPerson(ctx context.Context, id uuid.UUID) (*Person, error)
	CreatePerson(ctx context.Context, p Person) error
	UpdatePerson(ctx context.Context, p Person) error
	DeletePerson(ctx context.Context, id uuid.UUID) error

	ListRelationships(ctx context.Context) ([]RelationshipDTO, error)
	FindRelationship(ctx context.Context, id uuid.UUID) (*RelationshipDTO, error)
	CreateRelationship(ctx context.Context, r Relationship) error
	DeleteRelationship(ctx context.Context, id uuid.UUID) error
}

// Deriver builds the derived-kinship map for an origin. Injected rather than called directly so
// this package stays free of the BFS implementation and remains unit-testable with a stub. genders
// keys a subset (or none) of the reachable people by ID; a person absent from the map is treated
// as unknown gender by the deriver (defaults to male, matching the ported TS behavior).
type Deriver func(originID uuid.UUID, edges []personrelations.Edge, genders map[uuid.UUID]personrelations.Gender) map[uuid.UUID]personrelations.DerivedRelationship
