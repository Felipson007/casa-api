package jobexperience

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

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID map[uuid.UUID]Experience
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Experience)}
}

func (f *fakeRepository) List(_ context.Context, personID uuid.UUID) ([]Experience, error) {
	var result []Experience
	for _, e := range f.byID {
		if e.PersonID == personID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Experience, error) {
	e, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepository) Create(_ context.Context, e Experience) error {
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Update(_ context.Context, e Experience) error {
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

// fakePeopleRepository backs a real people.Service — jobexperience.Service takes the concrete
// type, same composition pattern as internal/personinfo, so tests need a real people.Service
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

func newTestService(t *testing.T) (svc *Service, personID, resumeDocumentID uuid.UUID) {
	t.Helper()
	personID = uuid.New()
	resumeDocumentID = uuid.New()

	peopleRepo := &fakePeopleRepository{people: map[uuid.UUID]people.Person{
		personID: {ID: personID, Name: "Test Person", Active: true},
	}}
	peopleSvc := people.NewService(peopleRepo, noopDeriver)

	docRepo := &fakeDocumentRepository{documents: map[uuid.UUID]documents.Document{
		resumeDocumentID: {ID: resumeDocumentID, OriginalFilename: "curriculo.pdf"},
	}}
	docSvc := documents.NewService(docRepo, nil, nil, nil, nil, nil, nil)

	repo := newFakeRepository()
	return NewService(repo, peopleSvc, docSvc), personID, resumeDocumentID
}

func date(s string) civildate.Date {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return civildate.Of(t)
}

func TestService_Create_CurrentJob(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created.IsCurrent {
		t.Fatal("expected IsCurrent = true for an experience with no EndDate")
	}
	if created.EndDate != nil {
		t.Fatalf("expected nil EndDate, got %v", created.EndDate)
	}
}

func TestService_Create_PastJobWithResume(t *testing.T) {
	service, personID, resumeDocumentID := newTestService(t)
	ctx := context.Background()
	end := date("2023-12-31")

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2020-01-15"),
		EndDate: &end, ResumeDocumentID: &resumeDocumentID,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.IsCurrent {
		t.Fatal("expected IsCurrent = false when EndDate is set")
	}
	if created.ResumeDocumentID == nil || *created.ResumeDocumentID != resumeDocumentID {
		t.Fatalf("expected ResumeDocumentID to round-trip, got %v", created.ResumeDocumentID)
	}
}

func TestService_Create_RejectsEndDateBeforeStartDate(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	end := date("2019-01-01")

	_, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2020-01-15"), EndDate: &end,
	})
	if err == nil {
		t.Fatal("expected an error for endDate before startDate")
	}
}

func TestService_Create_UnknownPerson(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: uuid.New(), Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown person, got %v", err)
	}
}

func TestService_Create_UnknownResumeDocument(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	bogusDoc := uuid.New()

	_, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
		ResumeDocumentID: &bogusDoc,
	})
	if !isNotFound(err) {
		t.Fatalf("expected apperr.NotFound for an unknown resume document, got %v", err)
	}
}

func TestService_Update_ClearingEndDateFlipsBackToCurrent(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	end := date("2023-12-31")

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2020-01-15"), EndDate: &end,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.Update(ctx, created.ID, UpdateExperienceRequest{
		Company: "Acme", Title: "Engenheiro Sênior", StartDate: date("2020-01-15"), EndDate: nil,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if !updated.IsCurrent {
		t.Fatal("expected clearing EndDate to flip IsCurrent back to true")
	}
	if updated.Title != "Engenheiro Sênior" {
		t.Fatalf("expected Title to update, got %q", updated.Title)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), UpdateExperienceRequest{
		Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
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

func TestService_Create_DefaultsContractTypeToCLT(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.ContractType != "CLT" {
		t.Fatalf("expected ContractType to default to CLT, got %q", created.ContractType)
	}
}

func TestService_Create_PJ_HasNoPayrollWithholding(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	salary := "10000.00"

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Consultor", StartDate: date("2024-01-15"),
		Salary: &salary, ContractType: "PJ",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.ContractType != "PJ" {
		t.Fatalf("expected ContractType = PJ, got %q", created.ContractType)
	}
	if created.PayrollDiscounts == nil {
		t.Fatal("expected PayrollDiscounts to be computed when Salary is set")
	}
	if created.PayrollDiscounts.INSS != "0.00" || created.PayrollDiscounts.IRRF != "0.00" {
		t.Fatalf("PJ must not carry INSS/IRRF withholding, got %+v", created.PayrollDiscounts)
	}
}

func TestService_Create_CLT_ComputesPayrollDiscounts(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	salary := "5000.00"

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
		Salary: &salary, ContractType: "CLT",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.PayrollDiscounts == nil {
		t.Fatal("expected PayrollDiscounts to be computed when Salary is set")
	}
	if created.PayrollDiscounts.INSS != "501.51" {
		t.Fatalf("expected INSS = 501.51 for a R$5,000 CLT salary, got %s", created.PayrollDiscounts.INSS)
	}
}

func TestService_Create_NoSalary_NoPayrollDiscounts(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "Acme", Title: "Engenheiro", StartDate: date("2024-01-15"),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.PayrollDiscounts != nil {
		t.Fatalf("expected nil PayrollDiscounts when no Salary is set, got %+v", created.PayrollDiscounts)
	}
}

func TestService_Create_USDAmountRoundTrips(t *testing.T) {
	service, personID, _ := newTestService(t)
	ctx := context.Background()
	salary, usd, rate := "9000.00", "1800.00", "5.0000"

	created, err := service.Create(ctx, CreateExperienceRequest{
		PersonID: personID, Company: "RemoteCo", Title: "Engenheiro", StartDate: date("2024-01-15"),
		Salary: &salary, USDAmount: &usd, ExchangeRate: &rate,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.USDAmount == nil || *created.USDAmount != usd {
		t.Fatalf("expected USDAmount to round-trip, got %v", created.USDAmount)
	}
	if created.ExchangeRate == nil || *created.ExchangeRate != rate {
		t.Fatalf("expected ExchangeRate to round-trip, got %v", created.ExchangeRate)
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

	if _, err := service.Create(ctx, CreateExperienceRequest{PersonID: personA, Company: "Acme", Title: "Eng", StartDate: date("2024-01-15")}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Create(ctx, CreateExperienceRequest{PersonID: personB, Company: "Beta", Title: "Eng", StartDate: date("2024-01-15")}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	list, err := service.List(ctx, personA)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 || list[0].Company != "Acme" {
		t.Fatalf("expected exactly the one experience for personA, got %+v", list)
	}
}
