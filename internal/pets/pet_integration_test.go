//go:build integration

package pets_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/pets"
	"github.com/atlas/atlas-go/internal/pets/postgres"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/pets/...
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
	service := pets.NewService(repo)

	created, err := service.Create(ctx, pets.CreatePetRequest{Name: "Integration Test Pet", Species: pets.SpeciesDog})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM pets WHERE id = $1`, created.ID) //nolint:errcheck

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Integration Test Pet" || !got.Active {
		t.Fatalf("unexpected pet after create: %+v", got)
	}

	updated, err := service.Update(ctx, created.ID, pets.UpdatePetRequest{Name: "Renamed", Species: pets.SpeciesCat, Active: false})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" || updated.Species != pets.SpeciesCat || updated.Active {
		t.Fatalf("unexpected pet after update: %+v", updated)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}
