package assets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID map[uuid.UUID]Asset
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Asset)}
}

func (f *fakeRepository) List(_ context.Context, personID, categoryID *uuid.UUID, status *Status) ([]Asset, error) {
	var result []Asset
	for _, a := range f.byID {
		if personID != nil && (a.PersonID == nil || *a.PersonID != *personID) {
			continue
		}
		if categoryID != nil && (a.CategoryID == nil || *a.CategoryID != *categoryID) {
			continue
		}
		if status != nil && a.Status != *status {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Asset, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func (f *fakeRepository) Create(_ context.Context, a Asset) error {
	f.byID[a.ID] = a
	return nil
}

func (f *fakeRepository) Update(_ context.Context, a Asset) error {
	if _, ok := f.byID[a.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[a.ID] = a
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateAssetRequest{Name: "Car"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Status != StatusActive {
		t.Fatalf("expected a newly created asset to be ACTIVE, got %v", created.Status)
	}
	if len(created.Attributes) != 0 {
		t.Fatalf("expected default attributes to be empty, got %v", created.Attributes)
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "Car" {
		t.Fatalf("expected name Car, got %q", got.Name)
	}

	list, err := service.List(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(list))
	}
}

func TestService_List_FiltersByStatus(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	active, err := service.Create(ctx, CreateAssetRequest{Name: "Still owned"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	sold, err := service.Create(ctx, CreateAssetRequest{Name: "Sold item"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Update(ctx, sold.ID, UpdateAssetRequest{Name: sold.Name, Status: StatusSold}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	activeStatus := StatusActive
	activeList, err := service.List(ctx, nil, nil, &activeStatus)
	if err != nil {
		t.Fatalf("List(status=ACTIVE) returned error: %v", err)
	}
	if len(activeList) != 1 || activeList[0].ID != active.ID {
		t.Fatalf("expected only the active asset back, got %+v", activeList)
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

	_, err := service.Update(ctx, uuid.New(), UpdateAssetRequest{Name: "Ghost", Status: StatusActive})

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
