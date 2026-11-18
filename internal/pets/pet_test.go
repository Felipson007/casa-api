package pets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID    map[uuid.UUID]Pet
	records map[uuid.UUID]Record
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Pet), records: make(map[uuid.UUID]Record)}
}

func (f *fakeRepository) List(_ context.Context, activeOnly *bool) ([]Pet, error) {
	var result []Pet
	for _, p := range f.byID {
		if activeOnly != nil && p.Active != *activeOnly {
			continue
		}
		result = append(result, p)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Pet, error) {
	p, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakeRepository) Create(_ context.Context, p Pet) error {
	f.byID[p.ID] = p
	return nil
}

func (f *fakeRepository) Update(_ context.Context, p Pet) error {
	if _, ok := f.byID[p.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[p.ID] = p
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeRepository) CreateRecord(_ context.Context, r Record) (Record, error) {
	f.records[r.ID] = r
	return r, nil
}

func (f *fakeRepository) ListRecordsByPet(_ context.Context, petID uuid.UUID) ([]Record, error) {
	var result []Record
	for _, r := range f.records {
		if r.PetID == petID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (f *fakeRepository) ListUpcomingVaccines(_ context.Context, petID uuid.UUID, today time.Time) ([]Record, error) {
	var result []Record
	for _, r := range f.records {
		if r.PetID == petID && r.Type == "VACCINE" && r.Status == RecordStatusPending && !r.RecordDate.Before(today) {
			result = append(result, r)
		}
	}
	return result, nil
}

func (f *fakeRepository) FindRecordByID(_ context.Context, id uuid.UUID) (*Record, error) {
	r, ok := f.records[id]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (f *fakeRepository) DeleteRecord(_ context.Context, id uuid.UUID) error {
	delete(f.records, id)
	return nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreatePetRequest{Name: "Rex", Species: SpeciesDog})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created.Active {
		t.Fatal("expected a newly created pet to be active")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "Rex" {
		t.Fatalf("expected name Rex, got %q", got.Name)
	}

	list, err := service.List(ctx, nil)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 pet, got %d", len(list))
	}
}

func TestService_List_FiltersByActive(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	active, err := service.Create(ctx, CreatePetRequest{Name: "Active Pet", Species: SpeciesCat})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	inactive, err := service.Create(ctx, CreatePetRequest{Name: "Soon Inactive", Species: SpeciesFish})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Update(ctx, inactive.ID, UpdatePetRequest{Name: inactive.Name, Species: inactive.Species, Active: false}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	activeOnly := true
	activeList, err := service.List(ctx, &activeOnly)
	if err != nil {
		t.Fatalf("List(active=true) returned error: %v", err)
	}
	if len(activeList) != 1 || activeList[0].ID != active.ID {
		t.Fatalf("expected only the active pet back, got %+v", activeList)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Get(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), UpdatePetRequest{Name: "Ghost", Species: SpeciesOther})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.Delete(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_CreateRecord_DefaultsStatusDone(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	pet, _ := service.Create(ctx, CreatePetRequest{Name: "Rex", Species: SpeciesDog})

	dto, err := service.CreateRecord(ctx, pet.ID, CreateRecordRequest{Type: "WEIGHT", RecordDate: civildate.Of(time.Now()), Amount: strPtr("12.5")})
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}
	if dto.Status != RecordStatusDone {
		t.Fatalf("expected default status DONE, got %s", dto.Status)
	}
}

func TestService_ListUpcomingVaccines_OnlyPendingVaccinesOnOrAfterToday(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	pet, _ := service.Create(ctx, CreatePetRequest{Name: "Rex", Species: SpeciesDog})
	now := time.Now()

	done := RecordStatusDone
	pending := RecordStatusPending
	if _, err := service.CreateRecord(ctx, pet.ID, CreateRecordRequest{Type: "VACCINE", RecordDate: civildate.Of(now.AddDate(0, 1, 0)), Status: &pending}); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}
	if _, err := service.CreateRecord(ctx, pet.ID, CreateRecordRequest{Type: "VACCINE", RecordDate: civildate.Of(now.AddDate(0, -1, 0)), Status: &done}); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}
	if _, err := service.CreateRecord(ctx, pet.ID, CreateRecordRequest{Type: "WEIGHT", RecordDate: civildate.Of(now.AddDate(0, 1, 0)), Status: &pending}); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	upcoming, err := service.ListUpcomingVaccines(ctx, pet.ID, now)
	if err != nil {
		t.Fatalf("ListUpcomingVaccines returned error: %v", err)
	}
	if len(upcoming) != 1 || upcoming[0].Type != "VACCINE" {
		t.Fatalf("expected exactly 1 pending future vaccine, got %+v", upcoming)
	}
}

func strPtr(s string) *string { return &s }
