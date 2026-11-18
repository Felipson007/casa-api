//go:build integration

package categories_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/categories/postgres"
)

// TestRepository_CreateListDelete exercises Handler -> Service -> postgres.Repository against a
// real Postgres — gated behind the "integration" build tag so plain `go test ./...` never touches
// a database (docs/architecture-plan.md section 8). Run with:
//
//	go test -tags=integration ./...
//
// against TEST_DATABASE_URL (never DATABASE_URL — mirrors the existing TEST_DB_URL/DB_URL split).
func TestRepository_CreateListDelete(t *testing.T) {
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
	service := categories.NewService(repo)

	expenseType := categories.TypeExpense
	created, err := service.Create(ctx, categories.CreateCategoryRequest{Name: "Rent", Type: &expenseType})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, created.ID) //nolint:errcheck

	list, err := service.List(ctx, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, c := range list {
		if c.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the just-created category back from List, got %+v", list)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
