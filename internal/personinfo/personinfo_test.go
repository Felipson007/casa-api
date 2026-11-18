package personinfo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personrelations"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID    map[uuid.UUID]Information
	history map[uuid.UUID][]HistoryEntry
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Information), history: make(map[uuid.UUID][]HistoryEntry)}
}

func (f *fakeRepository) List(_ context.Context, personID uuid.UUID) ([]Information, error) {
	var result []Information
	for _, i := range f.byID {
		if i.PersonID == personID {
			result = append(result, i)
		}
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Information, error) {
	i, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &i, nil
}

func (f *fakeRepository) Create(_ context.Context, i Information) error {
	f.byID[i.ID] = i
	return nil
}

func (f *fakeRepository) CreateBatch(_ context.Context, items []Information) error {
	for _, i := range items {
		f.byID[i.ID] = i
	}
	return nil
}

func (f *fakeRepository) Update(_ context.Context, i Information, snapshot *HistoryEntry) error {
	if _, ok := f.byID[i.ID]; !ok {
		return errors.New("not found")
	}
	if snapshot != nil {
		f.history[i.ID] = append(f.history[i.ID], *snapshot)
	}
	f.byID[i.ID] = i
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeRepository) ListHistory(_ context.Context, informationID uuid.UUID) ([]HistoryEntry, error) {
	return f.history[informationID], nil
}

func (f *fakeRepository) Equalize(_ context.Context, updates []EqualizeUpdate) error {
	for _, u := range updates {
		i, ok := f.byID[u.ID]
		if !ok {
			return errors.New("not found")
		}
		if u.Snapshot != nil {
			f.history[u.ID] = append(f.history[u.ID], *u.Snapshot)
		}
		i.Value = u.NewValue
		f.byID[u.ID] = i
	}
	return nil
}

// fakePeopleRepository backs a real people.Service — personinfo.Service takes the concrete type,
// same composition pattern as internal/dashboard.Service, so tests need a real people.Service
// instance rather than a hand-rolled interface stub.
type fakePeopleRepository struct {
	people map[uuid.UUID]people.Person
}

func (f *fakePeopleRepository) ListPeople(context.Context, string) ([]people.Person, error) {
	return nil, nil
}
func (f *fakePeopleRepository) FindPerson(_ context.Context, id uuid.UUID) (*people.Person, error) {
	p, ok := f.people[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}
func (f *fakePeopleRepository) CreatePerson(context.Context, people.Person) error { return nil }
func (f *fakePeopleRepository) UpdatePerson(context.Context, people.Person) error { return nil }
func (f *fakePeopleRepository) DeletePerson(context.Context, uuid.UUID) error     { return nil }
func (f *fakePeopleRepository) ListRelationships(context.Context) ([]people.RelationshipDTO, error) {
	return nil, nil
}
func (f *fakePeopleRepository) FindRelationship(context.Context, uuid.UUID) (*people.RelationshipDTO, error) {
	return nil, nil
}
func (f *fakePeopleRepository) CreateRelationship(context.Context, people.Relationship) error {
	return nil
}
func (f *fakePeopleRepository) DeleteRelationship(context.Context, uuid.UUID) error { return nil }

func noopDeriver(_ uuid.UUID, _ []personrelations.Edge, _ map[uuid.UUID]personrelations.Gender) map[uuid.UUID]personrelations.DerivedRelationship {
	return map[uuid.UUID]personrelations.DerivedRelationship{}
}

// fakeDocumentRepository backs a real documents.Service, same reasoning as fakePeopleRepository.
type fakeDocumentRepository struct {
	documents map[uuid.UUID]documents.Document
}

func (f *fakeDocumentRepository) List(context.Context, *uuid.UUID, *uuid.UUID, *uuid.UUID, string) ([]documents.Document, error) {
	return nil, nil
}
func (f *fakeDocumentRepository) FindByID(_ context.Context, id uuid.UUID) (*documents.Document, error) {
	d, ok := f.documents[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}
func (f *fakeDocumentRepository) Create(context.Context, documents.Document) error { return nil }
func (f *fakeDocumentRepository) Update(context.Context, documents.Document) error { return nil }
func (f *fakeDocumentRepository) Delete(context.Context, uuid.UUID) error          { return nil }
func (f *fakeDocumentRepository) UpdateFilePath(context.Context, uuid.UUID, string) error {
	return nil
}
func (f *fakeDocumentRepository) UpdateExtraction(context.Context, uuid.UUID, documents.ExtractionResult) error {
	return nil
}
func (f *fakeDocumentRepository) FindByContentHash(context.Context, string) (*documents.Document, error) {
	return nil, nil
}

func newTestService(t *testing.T) (*Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	personID := uuid.New()
	documentID := uuid.New()

	peopleRepo := &fakePeopleRepository{people: map[uuid.UUID]people.Person{
		personID: {ID: personID, Name: "Test Person", Active: true},
	}}
	peopleSvc := people.NewService(peopleRepo, noopDeriver)

	docRepo := &fakeDocumentRepository{documents: map[uuid.UUID]documents.Document{
		documentID: {ID: documentID, OriginalFilename: "rg.pdf"},
	}}
	docSvc := documents.NewService(docRepo, nil, nil, nil, nil, nil, nil)

	repo := newFakeRepository()
	return NewService(repo, peopleSvc, docSvc), personID, documentID
}

func TestService_Create_ManualOrigin(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateInformationRequest{
		PersonID: personID, Category: CategoryPersonal, Key: "RG", Value: "12.345.678-9",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Origin != OriginManual {
		t.Fatalf("expected OriginManual with no documentId, got %v", created.Origin)
	}
}

func TestService_Create_OCROriginWhenDocumentResolves(t *testing.T) {
	service, personID, documentID := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateInformationRequest{
		PersonID: personID, Category: CategoryPersonal, Key: "RG", Value: "12.345.678-9", DocumentID: &documentID,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Origin != OriginImport {
		t.Fatalf("expected OriginImport when documentId resolves, got %v", created.Origin)
	}
}

func TestService_Create_UnknownPerson(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Create(ctx, CreateInformationRequest{
		PersonID: uuid.New(), Category: CategoryPersonal, Key: "RG", Value: "x",
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown person, got %v", err)
	}
}

func TestService_Create_UnknownDocument(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	bogusDoc := uuid.New()
	_, err := service.Create(ctx, CreateInformationRequest{
		PersonID: personID, Category: CategoryPersonal, Key: "RG", Value: "x", DocumentID: &bogusDoc,
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown document, got %v", err)
	}
}

func TestService_CreateBatch_SharesOriginAcrossItems(t *testing.T) {
	service, personID, documentID := newTestService(t)
	ctx := context.Background()

	created, err := service.CreateBatch(ctx, CreateInformationBatchRequest{
		PersonID: personID, DocumentID: &documentID,
		Items: []CreateInformationBatchItem{
			{Category: CategoryPersonal, Key: "RG", Value: "1"},
			{Category: CategoryAcademic, Key: "Curso", Value: "Engenharia"},
		},
	})
	if err != nil {
		t.Fatalf("CreateBatch returned error: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 items, got %d", len(created))
	}
	for _, c := range created {
		if c.Origin != OriginImport {
			t.Fatalf("expected every batch item to share OriginImport, got %+v", c)
		}
	}
}

func TestService_Update_SnapshotsOnlyOnRealChange(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateInformationRequest{
		PersonID: personID, Category: CategoryPersonal, Key: "RG", Value: "1",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// No-op update (identical fields) must not create a history row.
	if _, err := service.Update(ctx, created.ID, UpdateInformationRequest{
		Category: CategoryPersonal, Key: "RG", Value: "1",
	}); err != nil {
		t.Fatalf("Update (no-op) returned error: %v", err)
	}
	history, err := service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory returned error: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no history entry from a no-op update, got %d", len(history))
	}

	// Real change must snapshot the OLD value.
	if _, err := service.Update(ctx, created.ID, UpdateInformationRequest{
		Category: CategoryPersonal, Key: "RG", Value: "2",
	}); err != nil {
		t.Fatalf("Update (real change) returned error: %v", err)
	}
	history, err = service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory returned error: %v", err)
	}
	if len(history) != 1 || history[0].Value != "1" {
		t.Fatalf("expected 1 history entry snapshotting the previous value '1', got %+v", history)
	}
}

func TestService_Equalize_SkipsAlreadyCorrectValues(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	a, err := service.Create(ctx, CreateInformationRequest{PersonID: personID, Category: CategoryPersonal, Key: "CPF", Value: "wrong"})
	if err != nil {
		t.Fatalf("Create a returned error: %v", err)
	}
	b, err := service.Create(ctx, CreateInformationRequest{PersonID: personID, Category: CategoryPersonal, Key: "CPF", Value: "correct"})
	if err != nil {
		t.Fatalf("Create b returned error: %v", err)
	}

	if err := service.Equalize(ctx, EqualizeInformationRequest{
		InformationIDs: []uuid.UUID{a.ID, b.ID}, Value: "correct",
	}); err != nil {
		t.Fatalf("Equalize returned error: %v", err)
	}

	gotA, err := service.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get a returned error: %v", err)
	}
	if gotA.Value != "correct" {
		t.Fatalf("expected a's value updated to 'correct', got %q", gotA.Value)
	}
	historyA, err := service.ListHistory(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListHistory a returned error: %v", err)
	}
	if len(historyA) != 1 || historyA[0].Value != "wrong" {
		t.Fatalf("expected a's history to snapshot 'wrong', got %+v", historyA)
	}

	historyB, err := service.ListHistory(ctx, b.ID)
	if err != nil {
		t.Fatalf("ListHistory b returned error: %v", err)
	}
	if len(historyB) != 0 {
		t.Fatalf("expected b (already 'correct') to have NO history entry, got %+v", historyB)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	err := service.Delete(ctx, uuid.New())
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound, got %v", err)
	}
}
