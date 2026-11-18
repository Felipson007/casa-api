package people

import (
	"context"
	"errors"
	"fmt"

	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/google/uuid"
)

// Service holds the business rules for people + relationships. Like categories.Service, every
// public method is O(1) database round-trips (section 13): no method loops calling the repository
// once per item. The only graph-shaped work — deriving indirect kinship — is an in-memory BFS over
// a single loaded edge list, not N queries.
type Service struct {
	repo    Repository
	deriver Deriver
}

func NewService(repo Repository, deriver Deriver) *Service {
	return &Service{repo: repo, deriver: deriver}
}

// ListPeople returns every person (single query). query, when non-empty, filters server-side to
// people whose name/nickname/email match it case-insensitively.
func (s *Service) ListPeople(ctx context.Context, query string) ([]Person, error) {
	people, err := s.repo.ListPeople(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("people: listing: %w", err)
	}
	return people, nil
}

// CreatePerson persists a new person with a gender (replacing the removed parentesco heuristic).
func (s *Service) CreatePerson(ctx context.Context, req CreatePersonRequest) (Person, error) {
	p := Person{
		ID:          uuid.New(),
		Name:        req.Name,
		Nickname:    req.Nickname,
		BirthDate:   req.BirthDate,
		Gender:      req.Gender,
		Notes:       req.Notes,
		Active:      true,
		Email:       req.Email,
		SelfKinship: req.SelfKinship,
	}
	if err := s.repo.CreatePerson(ctx, p); err != nil {
		return Person{}, fmt.Errorf("people: creating: %w", err)
	}
	return p, nil
}

// GetPerson returns a single person by id, or apperr.NotFound.
func (s *Service) GetPerson(ctx context.Context, id uuid.UUID) (Person, error) {
	p, err := s.repo.FindPerson(ctx, id)
	if err != nil {
		return Person{}, fmt.Errorf("people: finding: %w", err)
	}
	if p == nil {
		return Person{}, apperr.NotFound("person not found")
	}
	return *p, nil
}

// UpdatePerson replaces a person's editable fields (full-replace, mirrors clients.UpdateClient).
func (s *Service) UpdatePerson(ctx context.Context, id uuid.UUID, req UpdatePersonRequest) (Person, error) {
	existing, err := s.repo.FindPerson(ctx, id)
	if err != nil {
		return Person{}, fmt.Errorf("people: finding: %w", err)
	}
	if existing == nil {
		return Person{}, apperr.NotFound("person not found")
	}
	p := Person{
		ID:          id,
		Name:        req.Name,
		Nickname:    req.Nickname,
		BirthDate:   req.BirthDate,
		Gender:      req.Gender,
		Notes:       req.Notes,
		Active:      req.Active,
		Email:       req.Email,
		SelfKinship: existing.SelfKinship, // not part of the update form — never touched by a PUT
		CreatedAt:   existing.CreatedAt,
	}
	if err := s.repo.UpdatePerson(ctx, p); err != nil {
		return Person{}, fmt.Errorf("people: updating: %w", err)
	}
	return p, nil
}

// DeletePerson removes a person outright. Referencing rows (assets/documents/expenses/income/
// health_records/financial_balances) all have person_id ON DELETE SET NULL — deleting a person
// never cascades data loss elsewhere, it just orphans the reference (matches the old Java
// PessoaService.remover's own referential-integrity choice for the equivalent tables).
func (s *Service) DeletePerson(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindPerson(ctx, id)
	if err != nil {
		return fmt.Errorf("people: finding: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("person not found")
	}
	if err := s.repo.DeletePerson(ctx, id); err != nil {
		return fmt.Errorf("people: deleting: %w", err)
	}
	return nil
}

// ListRelationships returns every primary edge with resolved names (single query via the view).
func (s *Service) ListRelationships(ctx context.Context) ([]RelationshipDTO, error) {
	rels, err := s.repo.ListRelationships(ctx)
	if err != nil {
		return nil, fmt.Errorf("people: listing relationships: %w", err)
	}
	return rels, nil
}

// CreateRelationship validates the closed enum, rejects self-relation, enforces the gender rule
// (a woman cannot be FATHER_OF, a man cannot be MOTHER_OF — gender is now an explicit attribute of
// the person, not inferred), and rejects a direct cycle (mirroring PessoaRelacaoService), then
// persists the edge.
func (s *Service) CreateRelationship(ctx context.Context, req CreateRelationshipRequest) (RelationshipDTO, error) {
	if req.FromPersonID == req.ToPersonID {
		return RelationshipDTO{}, apperr.Validation("a person cannot relate to themselves", "toPersonId")
	}
	if !isValidRelType(req.Type) {
		return RelationshipDTO{}, apperr.Validation("invalid relationship type", "type")
	}

	// Gender integrity: a person's declared gender must be compatible with the edge they emanate.
	from, err := s.repo.FindPerson(ctx, req.FromPersonID)
	if err != nil {
		return RelationshipDTO{}, fmt.Errorf("people: looking up subject: %w", err)
	}
	if from == nil {
		return RelationshipDTO{}, apperr.NotFound("subject person not found")
	}
	if genderMismatch(req.Type, from.Gender) {
		return RelationshipDTO{}, apperr.Validation(
			"gender does not match the relationship type (a woman cannot be FATHER_OF, a man cannot be MOTHER_OF)", "type")
	}

	// Exact duplicate check runs before the cycle check: an edge that already connects from->to is
	// unavoidably "already reachable" in the undirected cycle graph below, which would otherwise
	// misreport a repeat of the very same edge as a cycle instead of the more specific conflict.
	existing, err := s.repo.ListRelationships(ctx)
	if err != nil {
		return RelationshipDTO{}, fmt.Errorf("people: loading graph for cycle check: %w", err)
	}
	for _, r := range existing {
		if r.FromPersonID == req.FromPersonID && r.ToPersonID == req.ToPersonID && r.Type == req.Type {
			return RelationshipDTO{}, apperr.Conflict("relationship already exists")
		}
	}

	// cycle check: from already a descendant of to, via the existing graph, would form a loop.
	edges := toPersonRelationsEdges(existing)
	if createsCycle(edges, req.FromPersonID, req.ToPersonID) {
		return RelationshipDTO{}, apperr.Validation("relationship would form a cycle", "toPersonId")
	}

	r := Relationship{
		ID:           uuid.New(),
		FromPersonID: req.FromPersonID,
		ToPersonID:   req.ToPersonID,
		Type:         req.Type,
	}
	if err := s.repo.CreateRelationship(ctx, r); err != nil {
		if errors.Is(err, ErrRelationshipExists) {
			return RelationshipDTO{}, apperr.Conflict("relationship already exists")
		}
		return RelationshipDTO{}, fmt.Errorf("people: creating relationship: %w", err)
	}
	// Re-read through the view to return names.
	created, err := s.repo.FindRelationship(ctx, r.ID)
	if err != nil {
		return RelationshipDTO{}, fmt.Errorf("people: reading created relationship: %w", err)
	}
	if created == nil {
		return RelationshipDTO{}, apperr.NotFound("relationship not found after create")
	}
	return *created, nil
}

// DeleteRelationship removes a primary edge (cascade of derived terms happens client-side via
// DeriveFromOrigin on the next read).
func (s *Service) DeleteRelationship(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindRelationship(ctx, id)
	if err != nil {
		return fmt.Errorf("people: looking up relationship: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("relationship not found")
	}
	if err := s.repo.DeleteRelationship(ctx, id); err != nil {
		return fmt.Errorf("people: deleting relationship: %w", err)
	}
	return nil
}

// DeriveFromOrigin returns the derived kinship of every person relative to originID. This is the
// replacement for the frontend's parentesco-grafo.ts BFS — one edge-list load + in-memory BFS.
func (s *Service) DeriveFromOrigin(ctx context.Context, originID uuid.UUID) ([]DerivedRelationshipDTO, error) {
	existing, err := s.repo.ListRelationships(ctx)
	if err != nil {
		return nil, fmt.Errorf("people: loading graph: %w", err)
	}
	people, err := s.repo.ListPeople(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("people: loading genders: %w", err)
	}
	genders := make(map[uuid.UUID]personrelations.Gender, len(people))
	for _, p := range people {
		genders[p.ID] = p.Gender
	}
	edges := toPersonRelationsEdges(existing)
	derived := s.deriver(originID, edges, genders)

	result := make([]DerivedRelationshipDTO, 0, len(derived))
	for pid, d := range derived {
		result = append(result, DerivedRelationshipDTO{
			PersonID:    pid,
			Parentesco:  d.Parentesco,
			RotuloLivre: d.RotuloLivre,
		})
	}
	return result, nil
}

func isValidRelType(t personrelations.RelType) bool {
	switch t {
	case personrelations.MotherOf, personrelations.FatherOf, personrelations.SpouseOf:
		return true
	}
	return false
}

// genderMismatch reports whether the edge type is impossible for the subject's declared gender.
// gender is an explicit attribute of the person (see people.Person.Gender), so this is a hard
// integrity rule, not an inference: a woman cannot emanate FATHER_OF, a man cannot emanate
// MOTHER_OF. SPOUSE_OF carries no gender constraint.
func genderMismatch(t personrelations.RelType, g personrelations.Gender) bool {
	switch t {
	case personrelations.FatherOf:
		return g == personrelations.GenderFemale
	case personrelations.MotherOf:
		return g == personrelations.GenderMale
	}
	return false
}

// createsCycle reports whether adding from->to would create a loop, given existing edges.
// A->B would cycle if B already reaches A through parent/child/spouse edges.
func createsCycle(existing []personrelations.Edge, from, to uuid.UUID) bool {
	// Build adjacency both directions (undirected for cycle purposes).
	adj := map[uuid.UUID][]uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	var walk func(cur uuid.UUID)
	walk = func(cur uuid.UUID) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		for _, n := range adj[cur] {
			walk(n)
		}
	}
	for _, e := range existing {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	// If from is already reachable from to, adding from->to closes a loop.
	for _, n := range adj[to] {
		if n == from {
			return true
		}
		// walk from 'to'; if we can reach 'from', cycle.
		for k := range seen {
			delete(seen, k)
		}
		walk(to)
		if seen[from] {
			return true
		}
	}
	return false
}

func toPersonRelationsEdges(rels []RelationshipDTO) []personrelations.Edge {
	edges := make([]personrelations.Edge, 0, len(rels))
	for _, r := range rels {
		edges = append(edges, personrelations.Edge{
			From: r.FromPersonID,
			To:   r.ToPersonID,
			Type: r.Type,
		})
	}
	return edges
}
