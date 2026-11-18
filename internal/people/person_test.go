package people

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved, so these
// run as part of the default `go test ./...` (mirrors clients.fakeRepository's pattern).
type fakeRepository struct {
	people map[uuid.UUID]Person
	rels   map[uuid.UUID]Relationship
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{people: make(map[uuid.UUID]Person), rels: make(map[uuid.UUID]Relationship)}
}

func (f *fakeRepository) ListPeople(_ context.Context, query string) ([]Person, error) {
	var result []Person
	for _, p := range f.people {
		if query != "" && !personMatchesQuery(p, query) {
			continue
		}
		result = append(result, p)
	}
	return result, nil
}

func personMatchesQuery(p Person, query string) bool {
	q := strings.ToLower(query)
	if strings.Contains(strings.ToLower(p.Name), q) {
		return true
	}
	if p.Nickname != nil && strings.Contains(strings.ToLower(*p.Nickname), q) {
		return true
	}
	if p.Email != nil && strings.Contains(strings.ToLower(*p.Email), q) {
		return true
	}
	return false
}

func (f *fakeRepository) FindPerson(_ context.Context, id uuid.UUID) (*Person, error) {
	p, ok := f.people[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakeRepository) CreatePerson(_ context.Context, p Person) error {
	f.people[p.ID] = p
	return nil
}

func (f *fakeRepository) UpdatePerson(_ context.Context, p Person) error {
	if _, ok := f.people[p.ID]; !ok {
		return ErrPersonNotFound
	}
	f.people[p.ID] = p
	return nil
}

func (f *fakeRepository) DeletePerson(_ context.Context, id uuid.UUID) error {
	delete(f.people, id)
	return nil
}

func (f *fakeRepository) ListRelationships(_ context.Context) ([]RelationshipDTO, error) {
	var result []RelationshipDTO
	for _, r := range f.rels {
		result = append(result, f.toDTO(r))
	}
	return result, nil
}

func (f *fakeRepository) FindRelationship(_ context.Context, id uuid.UUID) (*RelationshipDTO, error) {
	r, ok := f.rels[id]
	if !ok {
		return nil, nil
	}
	dto := f.toDTO(r)
	return &dto, nil
}

func (f *fakeRepository) CreateRelationship(_ context.Context, r Relationship) error {
	for _, existing := range f.rels {
		if existing.FromPersonID == r.FromPersonID && existing.ToPersonID == r.ToPersonID && existing.Type == r.Type {
			return ErrRelationshipExists
		}
	}
	f.rels[r.ID] = r
	return nil
}

func (f *fakeRepository) DeleteRelationship(_ context.Context, id uuid.UUID) error {
	delete(f.rels, id)
	return nil
}

func (f *fakeRepository) toDTO(r Relationship) RelationshipDTO {
	dto := RelationshipDTO{ID: r.ID, FromPersonID: r.FromPersonID, Type: r.Type, ToPersonID: r.ToPersonID}
	if p, ok := f.people[r.FromPersonID]; ok {
		dto.FromName = p.Name
	}
	if p, ok := f.people[r.ToPersonID]; ok {
		dto.ToName = p.Name
	}
	return dto
}

// noopDeriver is a stub Deriver for tests that don't exercise DeriveFromOrigin's actual BFS
// (that logic already has its own dedicated coverage in internal/personrelations/derive_test.go).
func noopDeriver(_ uuid.UUID, _ []personrelations.Edge, _ map[uuid.UUID]personrelations.Gender) map[uuid.UUID]personrelations.DerivedRelationship {
	return map[uuid.UUID]personrelations.DerivedRelationship{}
}

func mustCreatePerson(t *testing.T, s *Service, ctx context.Context, name string, gender personrelations.Gender) Person {
	t.Helper()
	p, err := s.CreatePerson(ctx, CreatePersonRequest{Name: name, Gender: gender})
	if err != nil {
		t.Fatalf("CreatePerson(%q) returned error: %v", name, err)
	}
	return p
}

func TestService_CreateGetPerson(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	created := mustCreatePerson(t, service, ctx, "Alice", personrelations.GenderFemale)
	if !created.Active {
		t.Fatal("expected a newly created person to be active")
	}

	got, err := service.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson returned error: %v", err)
	}
	if got.Name != "Alice" {
		t.Fatalf("expected name Alice, got %q", got.Name)
	}
}

func TestService_CreatePerson_PersistsSelfKinship(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	kinship := LegacyKinshipSelf
	created, err := service.CreatePerson(ctx, CreatePersonRequest{Name: "Alice", SelfKinship: &kinship})
	if err != nil {
		t.Fatalf("CreatePerson returned error: %v", err)
	}
	if created.SelfKinship == nil || *created.SelfKinship != LegacyKinshipSelf {
		t.Fatalf("expected SelfKinship to round-trip as %q, got %v", LegacyKinshipSelf, created.SelfKinship)
	}
}

func TestService_UpdatePerson_NeverChangesSelfKinship(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	kinship := LegacyKinshipSelf
	created, err := service.CreatePerson(ctx, CreatePersonRequest{Name: "Alice", SelfKinship: &kinship})
	if err != nil {
		t.Fatalf("CreatePerson returned error: %v", err)
	}

	updated, err := service.UpdatePerson(ctx, created.ID, UpdatePersonRequest{Name: "Alice Updated", Active: true})
	if err != nil {
		t.Fatalf("UpdatePerson returned error: %v", err)
	}
	if updated.SelfKinship == nil || *updated.SelfKinship != LegacyKinshipSelf {
		t.Fatalf("expected SelfKinship to survive an update untouched, got %v", updated.SelfKinship)
	}
}

func TestService_ListPeople_FiltersByQuery(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	nick := "Bibi"
	email := "carol@example.com"
	repo.people[uuid.New()] = Person{ID: uuid.New(), Name: "Alice Ferreira", Active: true}
	repo.people[uuid.New()] = Person{ID: uuid.New(), Name: "Bruna Souza", Nickname: &nick, Active: true}
	repo.people[uuid.New()] = Person{ID: uuid.New(), Name: "Carol Lima", Email: &email, Active: true}

	all, err := service.ListPeople(ctx, "")
	if err != nil {
		t.Fatalf("ListPeople(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 people with no query, got %d", len(all))
	}

	byName, err := service.ListPeople(ctx, "ferreira")
	if err != nil {
		t.Fatalf("ListPeople(name): %v", err)
	}
	if len(byName) != 1 || byName[0].Name != "Alice Ferreira" {
		t.Fatalf("expected only Alice Ferreira for query 'ferreira', got %+v", byName)
	}

	byNick, err := service.ListPeople(ctx, "bib")
	if err != nil {
		t.Fatalf("ListPeople(nick): %v", err)
	}
	if len(byNick) != 1 || byNick[0].Name != "Bruna Souza" {
		t.Fatalf("expected Bruna Souza by nickname, got %+v", byNick)
	}

	byEmail, err := service.ListPeople(ctx, "CAROL@EXAMPLE")
	if err != nil {
		t.Fatalf("ListPeople(email): %v", err)
	}
	if len(byEmail) != 1 || byEmail[0].Name != "Carol Lima" {
		t.Fatalf("expected Carol Lima by email (case-insensitive), got %+v", byEmail)
	}
}

func TestService_GetPerson_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	_, err := service.GetPerson(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_UpdatePerson(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	created := mustCreatePerson(t, service, ctx, "Bob", personrelations.GenderMale)

	updated, err := service.UpdatePerson(ctx, created.ID, UpdatePersonRequest{
		Name: "Bob Updated", Gender: personrelations.GenderMale, Active: false,
	})
	if err != nil {
		t.Fatalf("UpdatePerson returned error: %v", err)
	}
	if updated.Name != "Bob Updated" || updated.Active {
		t.Fatalf("expected updated fields to persist, got %+v", updated)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatal("expected CreatedAt to be preserved across an update, not reset")
	}
}

func TestService_UpdatePerson_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	_, err := service.UpdatePerson(ctx, uuid.New(), UpdatePersonRequest{Name: "Ghost"})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_DeletePerson(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	created := mustCreatePerson(t, service, ctx, "Carol", personrelations.GenderFemale)

	if err := service.DeletePerson(ctx, created.ID); err != nil {
		t.Fatalf("DeletePerson returned error: %v", err)
	}
	if _, err := service.GetPerson(ctx, created.ID); err == nil {
		t.Fatal("expected the person to be gone after DeletePerson")
	}
}

func TestService_DeletePerson_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	err := service.DeletePerson(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_CreateRelationship_RejectsSelfRelation(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	p := mustCreatePerson(t, service, ctx, "Solo", personrelations.GenderMale)

	_, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: p.ID, ToPersonID: p.ID, Type: personrelations.FatherOf,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for self-relation, got %v", err)
	}
}

func TestService_CreateRelationship_RejectsInvalidType(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	a := mustCreatePerson(t, service, ctx, "A", personrelations.GenderMale)
	b := mustCreatePerson(t, service, ctx, "B", personrelations.GenderFemale)

	_, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: a.ID, ToPersonID: b.ID, Type: "SIBLING_OF",
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for an invalid relationship type, got %v", err)
	}
}

func TestService_CreateRelationship_RejectsGenderMismatch(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	woman := mustCreatePerson(t, service, ctx, "Woman", personrelations.GenderFemale)
	child := mustCreatePerson(t, service, ctx, "Child", personrelations.GenderMale)

	_, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: woman.ID, ToPersonID: child.ID, Type: personrelations.FatherOf,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for a woman as FATHER_OF, got %v", err)
	}
}

func TestService_CreateRelationship_RejectsCycle(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	grandparent := mustCreatePerson(t, service, ctx, "Grandparent", personrelations.GenderMale)
	parent := mustCreatePerson(t, service, ctx, "Parent", personrelations.GenderMale)
	self := mustCreatePerson(t, service, ctx, "Self", personrelations.GenderMale)

	if _, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: grandparent.ID, ToPersonID: parent.ID, Type: personrelations.FatherOf,
	}); err != nil {
		t.Fatalf("seeding grandparent->parent edge failed: %v", err)
	}
	if _, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: parent.ID, ToPersonID: self.ID, Type: personrelations.FatherOf,
	}); err != nil {
		t.Fatalf("seeding parent->self edge failed: %v", err)
	}

	// self -> grandparent would close a loop through the two edges just created.
	_, err := service.CreateRelationship(ctx, CreateRelationshipRequest{
		FromPersonID: self.ID, ToPersonID: grandparent.ID, Type: personrelations.FatherOf,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for a cycle, got %v", err)
	}
}

func TestService_CreateRelationship_RejectsDuplicate(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	father := mustCreatePerson(t, service, ctx, "Father", personrelations.GenderMale)
	child := mustCreatePerson(t, service, ctx, "Child", personrelations.GenderMale)

	req := CreateRelationshipRequest{FromPersonID: father.ID, ToPersonID: child.ID, Type: personrelations.FatherOf}
	if _, err := service.CreateRelationship(ctx, req); err != nil {
		t.Fatalf("first CreateRelationship returned error: %v", err)
	}

	_, err := service.CreateRelationship(ctx, req)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindConflict {
		t.Fatalf("expected a conflict apperr.Error for a duplicate relationship, got %v", err)
	}
}

func TestService_DeleteRelationship_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, noopDeriver)
	ctx := context.Background()

	err := service.DeleteRelationship(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}
