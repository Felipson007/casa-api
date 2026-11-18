package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	logs map[uuid.UUID]AuditLog
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{logs: make(map[uuid.UUID]AuditLog)}
}

func (f *fakeRepository) Create(_ context.Context, a AuditLog) error {
	f.logs[a.ID] = a
	return nil
}

func (f *fakeRepository) List(_ context.Context, tableName string, rowID *uuid.UUID) ([]AuditLog, error) {
	var result []AuditLog
	for _, a := range f.logs {
		if tableName != "" && a.TableName != tableName {
			continue
		}
		if rowID != nil && a.RowID != *rowID {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*AuditLog, error) {
	a, ok := f.logs[id]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateAuditLogRequest{
		TableName: "people",
		RowID:     uuid.New(),
		OldData:   map[string]interface{}{"name": "Old Name"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected a generated ID")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.TableName != "people" {
		t.Fatalf("expected table_name 'people', got %q", got.TableName)
	}

	all, err := service.List(ctx, "", nil)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 log, got %d", len(all))
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

func TestService_List_FiltersByTableAndRow(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	row1 := uuid.New()
	row2 := uuid.New()
	_, _ = service.Create(ctx, CreateAuditLogRequest{TableName: "people", RowID: row1, OldData: map[string]interface{}{}})
	_, _ = service.Create(ctx, CreateAuditLogRequest{TableName: "assets", RowID: row2, OldData: map[string]interface{}{}})

	byTable, err := service.List(ctx, "people", nil)
	if err != nil {
		t.Fatalf("List by table error: %v", err)
	}
	if len(byTable) != 1 {
		t.Fatalf("expected 1 'people' log, got %d", len(byTable))
	}

	byRow, err := service.List(ctx, "", &row2)
	if err != nil {
		t.Fatalf("List by row error: %v", err)
	}
	if len(byRow) != 1 {
		t.Fatalf("expected 1 log for row2, got %d", len(byRow))
	}
	if byRow[0].TableName != "assets" {
		t.Fatalf("expected 'assets' log for row2, got %q", byRow[0].TableName)
	}
}
