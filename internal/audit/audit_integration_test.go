//go:build integration

package audit_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/audit"
	"github.com/atlas/atlas-go/internal/audit/postgres"
)

// TestRepository_CreateGetList exercises Service -> postgres.Repository against a real Postgres —
// gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/audit/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetList(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	repo := postgres.NewRepository(pool)
	service := audit.NewService(repo)

	rowID := uuid.New()
	created, err := service.Create(ctx, audit.CreateAuditLogRequest{
		TableName: "people",
		RowID:     rowID,
		OldData:   map[string]interface{}{"name": "Old Name", "gender": "M"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM audit_logs WHERE id = $1`, created.ID) //nolint:errcheck

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TableName != "people" {
		t.Fatalf("expected table_name 'people', got %q", got.TableName)
	}
	if got.OldData["name"] != "Old Name" {
		t.Fatalf("expected old_data.name 'Old Name', got %v", got.OldData["name"])
	}

	byTable, err := service.List(ctx, "people", nil)
	if err != nil {
		t.Fatalf("List by table: %v", err)
	}
	if len(byTable) == 0 {
		t.Fatal("expected at least 1 'people' log")
	}

	byRow, err := service.List(ctx, "", &rowID)
	if err != nil {
		t.Fatalf("List by row: %v", err)
	}
	if len(byRow) != 1 {
		t.Fatalf("expected exactly 1 log for the test row, got %d", len(byRow))
	}
}
