//go:build integration

package people_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/people/postgres"
)

// TestRepository_ListPeople_ReturnsEmptySliceNeverNil is a regression test: ListPeople used to
// declare `var result []people.Person` and only append inside the row loop, so a query matching
// zero rows returned a Go nil slice. json.Encode serializes nil as `null`, and the frontend's
// nameLookups (lib/api/gastos.ts, receitas.ts) does `cats.map(...)` on the /api/people response
// unconditionally — a null response crashed with "Cannot read properties of null (reading 'map')"
// in production. Run with:
//
//	go test -tags=integration ./internal/people/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_ListPeople_ReturnsEmptySliceNeverNil(t *testing.T) {
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

	// A search query that cannot match any real person name yields zero rows — the exact
	// condition that exposed the nil slice.
	result, err := repo.ListPeople(ctx, "no-such-person-xyz-integration-test-guard")
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if result == nil {
		t.Fatal("ListPeople returned a nil slice for zero matches — must be an empty, non-nil slice so it serializes as [] instead of null")
	}
	if len(result) != 0 {
		t.Fatalf("expected zero matches, got %d", len(result))
	}
}

// TestRepository_ListRelationships_ReturnsEmptySliceNeverNil mirrors the ListPeople guard above
// for the sibling query — same nil-slice bug, same file, same fix.
func TestRepository_ListRelationships_ReturnsEmptySliceNeverNil(t *testing.T) {
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

	result, err := repo.ListRelationships(ctx)
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	if result == nil {
		t.Fatal("ListRelationships returned a nil slice — must be an empty, non-nil slice so a household with no relationships serializes as [] instead of null")
	}
}
