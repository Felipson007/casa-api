package clients

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved, so these
// run as part of the default `go test ./...`.
type fakeRepository struct {
	byID map[uuid.UUID]Client
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Client)}
}

func (f *fakeRepository) List(_ context.Context, activeOnly *bool, _ string) ([]Client, error) {
	var result []Client
	for _, c := range f.byID {
		if activeOnly != nil && c.Active != *activeOnly {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Client, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (f *fakeRepository) Create(_ context.Context, c Client) error {
	f.byID[c.ID] = c
	return nil
}

func (f *fakeRepository) Update(_ context.Context, c Client) error {
	if _, ok := f.byID[c.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[c.ID] = c
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

	phone := "+55 11 91234-5678"
	created, err := service.Create(ctx, CreateClientRequest{Name: "Alice", Phone: &phone})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created.Active {
		t.Fatal("expected a newly created client to be active")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "Alice" {
		t.Fatalf("expected name Alice, got %q", got.Name)
	}

	list, err := service.List(ctx, nil, "")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 client, got %d", len(list))
	}
}

func TestService_List_FiltersByActive(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	active, err := service.Create(ctx, CreateClientRequest{Name: "Active Client"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	inactive, err := service.Create(ctx, CreateClientRequest{Name: "Soon Inactive"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Update(ctx, inactive.ID, UpdateClientRequest{Name: inactive.Name, Active: false}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	activeOnly := true
	activeList, err := service.List(ctx, &activeOnly, "")
	if err != nil {
		t.Fatalf("List(active=true) returned error: %v", err)
	}
	if len(activeList) != 1 || activeList[0].ID != active.ID {
		t.Fatalf("expected only the active client back, got %+v", activeList)
	}

	inactiveOnly := false
	inactiveList, err := service.List(ctx, &inactiveOnly, "")
	if err != nil {
		t.Fatalf("List(active=false) returned error: %v", err)
	}
	if len(inactiveList) != 1 || inactiveList[0].ID != inactive.ID {
		t.Fatalf("expected only the inactive client back, got %+v", inactiveList)
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

	_, err := service.Update(ctx, uuid.New(), UpdateClientRequest{Name: "Ghost"})

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
