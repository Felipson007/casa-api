//go:build integration

package backup_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/backup"
	"github.com/atlas/atlas-go/internal/backup/postgres"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/backup/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetUpdateDelete(t *testing.T) {
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
	service := backup.NewService(repo)

	created, err := service.Create(ctx, backup.CreateBackupConfigRequest{Hour: 3, Minute: 0})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM backup_configuration WHERE id = $1`, created.ID) //nolint:errcheck

	if created.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be populated from the DB default, not zero-value (F-3 regression)")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Hour != 3 || got.Minute != 0 {
		t.Fatalf("expected hour=3 minute=0 round-tripped through Postgres, got %+v", got)
	}

	// minute=0 must round-trip through Update too (regression test for the required+min=0 bug).
	updated, err := service.Update(ctx, created.ID, backup.UpdateBackupConfigRequest{Hour: 4, Minute: 0})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Hour != 4 || updated.Minute != 0 {
		t.Fatalf("expected hour=4 minute=0 after update, got %+v", updated)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}
