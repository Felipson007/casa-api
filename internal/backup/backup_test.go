package backup

import (
	"context"
	"errors"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	rows map[string]*BackupConfig
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{rows: make(map[string]*BackupConfig)}
}

func (f *fakeRepository) List(_ context.Context) ([]BackupConfig, error) {
	var result []BackupConfig
	for _, c := range f.rows {
		result = append(result, *c)
	}
	return result, nil
}

func (f *fakeRepository) Create(_ context.Context, c *BackupConfig) (*BackupConfig, error) {
	cp := *c
	f.rows[c.ID] = &cp
	return &cp, nil
}

func (f *fakeRepository) Get(_ context.Context, id string) (*BackupConfig, error) {
	c, ok := f.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (f *fakeRepository) Update(_ context.Context, c *BackupConfig) (*BackupConfig, error) {
	if _, ok := f.rows[c.ID]; !ok {
		return nil, ErrNotFound
	}
	cp := *c
	f.rows[c.ID] = &cp
	return &cp, nil
}

func (f *fakeRepository) Delete(_ context.Context, id string) error {
	if _, ok := f.rows[id]; !ok {
		return ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func TestService_CreateGetListUpdateDelete(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateBackupConfigRequest{Hour: 3, Minute: 0})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Hour != 3 || created.Minute != 0 {
		t.Fatalf("expected hour=3 minute=0, got %+v", created)
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected id %v, got %v", created.ID, got.ID)
	}

	list, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 config, got %d", len(list))
	}

	// minute=0 is a legitimate value (midnight-ish schedules), not a missing field — regression
	// test for the "required + min=0 rejects zero" bug Hermes found and fixed in the DTO.
	updated, err := service.Update(ctx, created.ID, UpdateBackupConfigRequest{Hour: 2, Minute: 0})
	if err != nil {
		t.Fatalf("Update (minute=0) returned error: %v", err)
	}
	if updated.Hour != 2 || updated.Minute != 0 {
		t.Fatalf("expected hour=2 minute=0 after update, got %+v", updated)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); !isNotFound(err) {
		t.Fatalf("expected NotFound after Delete, got %v", err)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)

	_, err := service.Get(context.Background(), "nonexistent")
	if !isNotFound(err) {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)

	_, err := service.Update(context.Background(), "nonexistent", UpdateBackupConfigRequest{Hour: 1, Minute: 1})
	if !isNotFound(err) {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)

	err := service.Delete(context.Background(), "nonexistent")
	if !isNotFound(err) {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}
