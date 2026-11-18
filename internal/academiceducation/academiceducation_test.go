package academiceducation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved. Mirrors
// internal/jobexperience's own fake exactly.
type fakeRepository struct {
	byID map[uuid.UUID]Education
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Education)}
}

func (f *fakeRepository) List(_ context.Context, personID uuid.UUID) ([]Education, error) {
	var result []Education
	for _, e := range f.byID {
		if e.PersonID == personID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Education, error) {
	e, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepository) Create(_ context.Context, e Education) error {
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Update(_ context.Context, e Education) error {
	if _, ok := f.byID[e.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

// fakePeopleRepository backs a real people.Service — same reasoning as internal/jobexperience's
// own fake (Service takes the concrete type, not a hand-rolled interface stub).
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

func newTestService(t *testing.T) (svc *Service, personID, diplomaDocumentID uuid.UUID) {
	t.Helper()
	personID = uuid.New()
	diplomaDocumentID = uuid.New()

	peopleRepo := &fakePeopleRepository{people: map[uuid.UUID]people.Person{
		personID: {ID: personID, Name: "Test Person", Active: true},
	}}
	peopleSvc := people.NewService(peopleRepo, noopDeriver)

	docRepo := &fakeDocumentRepository{documents: map[uuid.UUID]documents.Document{
		diplomaDocumentID: {ID: diplomaDocumentID, OriginalFilename: "diploma.pdf"},
	}}
	docSvc := documents.NewService(docRepo, nil, nil, nil, nil, nil, nil)

	repo := newFakeRepository()
	return NewService(repo, peopleSvc, docSvc), personID, diplomaDocumentID
}

func date(s string) civildate.Date {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return civildate.Of(t)
}

func TestService_Create_CurrentEducation(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateEducationRequest{
		PersonID: personID, Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-02-01"),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created.IsCurrent {
		t.Fatal("expected IsCurrent = true for an education record with no EndDate")
	}
}

func TestService_Create_PastEducationWithDiploma(t *testing.T) {
	service, personID, diplomaDocumentID := newTestService(t)
	ctx := context.Background()
	end := date("2023-12-15")

	created, err := service.Create(ctx, CreateEducationRequest{
		PersonID: personID, Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-02-01"),
		EndDate: &end, DiplomaDocumentID: &diplomaDocumentID,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.IsCurrent {
		t.Fatal("expected IsCurrent = false when EndDate is set")
	}
	if created.DiplomaDocumentID == nil || *created.DiplomaDocumentID != diplomaDocumentID {
		t.Fatalf("expected DiplomaDocumentID to round-trip, got %v", created.DiplomaDocumentID)
	}
}

func TestService_Create_RejectsEndDateBeforeStartDate(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	end := date("2019-01-01")

	_, err := service.Create(ctx, CreateEducationRequest{
		PersonID: personID, Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-01-15"), EndDate: &end,
	})
	if err == nil {
		t.Fatal("expected an error for endDate before startDate")
	}
}

func TestService_Create_UnknownPerson(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Create(ctx, CreateEducationRequest{
		PersonID: uuid.New(), Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-01-15"),
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown person, got %v", err)
	}
}

func TestService_Create_UnknownDiplomaDocument(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	bogusDoc := uuid.New()

	_, err := service.Create(ctx, CreateEducationRequest{
		PersonID: personID, Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-01-15"),
		DiplomaDocumentID: &bogusDoc,
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown diploma document, got %v", err)
	}
}

func TestService_Update_ClearingEndDateFlipsBackToCurrent(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	end := date("2023-12-15")

	created, err := service.Create(ctx, CreateEducationRequest{
		PersonID: personID, Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-01-15"), EndDate: &end,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.Update(ctx, created.ID, UpdateEducationRequest{
		Institution: "USP", Course: "Mestrado em Ciência da Computação", StartDate: date("2020-01-15"), EndDate: nil,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if !updated.IsCurrent {
		t.Fatal("expected clearing EndDate to flip IsCurrent back to true")
	}
	if updated.Course != "Mestrado em Ciência da Computação" {
		t.Fatalf("expected Course to update, got %q", updated.Course)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), UpdateEducationRequest{
		Institution: "USP", Course: "Ciência da Computação", StartDate: date("2020-01-15"),
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	if err := service.Delete(ctx, uuid.New()); !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound, got %v", err)
	}
}

func TestService_List_FiltersByPerson(t *testing.T) {
	ctx := context.Background()
	personA, personB := uuid.New(), uuid.New()
	peopleRepo := &fakePeopleRepository{people: map[uuid.UUID]people.Person{
		personA: {ID: personA, Active: true}, personB: {ID: personB, Active: true},
	}}
	peopleSvc := people.NewService(peopleRepo, noopDeriver)
	docSvc := documents.NewService(&fakeDocumentRepository{}, nil, nil, nil, nil, nil, nil)
	service := NewService(newFakeRepository(), peopleSvc, docSvc)

	if _, err := service.Create(ctx, CreateEducationRequest{PersonID: personA, Institution: "USP", Course: "Eng", StartDate: date("2020-01-15")}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Create(ctx, CreateEducationRequest{PersonID: personB, Institution: "Unicamp", Course: "Eng", StartDate: date("2020-01-15")}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	list, err := service.List(ctx, personA)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 || list[0].Institution != "USP" {
		t.Fatalf("expected exactly the one education record for personA, got %+v", list)
	}
}
