package categories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved, so these
// run as part of the default `go test ./...` (docs/architecture-plan.md section 8).
type fakeRepository struct {
	byID map[uuid.UUID]Category
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Category)}
}

func (f *fakeRepository) List(_ context.Context, filterType *Type) ([]Category, error) {
	var result []Category
	for _, c := range f.byID {
		if filterType != nil && (c.Type == nil || *c.Type != *filterType) {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Category, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (f *fakeRepository) Create(_ context.Context, c Category) error {
	f.byID[c.ID] = c
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeRepository) CountAll(_ context.Context) (int, error) {
	return len(f.byID), nil
}

func TestService_CreateAndList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	expenseType := TypeExpense
	dto, err := service.Create(ctx, CreateCategoryRequest{Name: "Groceries", Type: &expenseType})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if dto.Name != "Groceries" {
		t.Fatalf("expected name Groceries, got %q", dto.Name)
	}

	list, err := service.List(ctx, nil)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 category, got %d", len(list))
	}
}

func TestService_Create_RejectsUnknownParent(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	unknownParent := uuid.New()
	_, err := service.Create(ctx, CreateCategoryRequest{Name: "Sub", ParentCategoryID: &unknownParent})

	var appErr *apperr.Error
	if err == nil {
		t.Fatal("expected an error for an unknown parent category")
	}
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error, got %v", err)
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

func TestService_SeedDefaults_OnlyWhenEmpty(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	if err := service.SeedDefaults(ctx); err != nil {
		t.Fatalf("SeedDefaults returned error: %v", err)
	}
	firstCount, _ := repo.CountAll(ctx)
	if firstCount == 0 {
		t.Fatal("expected default categories to be seeded")
	}

	// A second call must be a no-op — there are already categories.
	if err := service.SeedDefaults(ctx); err != nil {
		t.Fatalf("second SeedDefaults returned error: %v", err)
	}
	secondCount, _ := repo.CountAll(ctx)
	if secondCount != firstCount {
		t.Fatalf("expected seeding to be idempotent, got %d then %d categories", firstCount, secondCount)
	}
}
