//go:build integration

package clients_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/clients/postgres"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag, per docs/test-seed.md: apply
// internal/migrations/000001_init_schema.up.sql first (and optionally load testdata/seed.sql).
// Run with:
//
//	go test -tags=integration ./internal/clients/...
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
	service := clients.NewService(repo)

	phone := "+55 11 90000-0000"
	created, err := service.Create(ctx, clients.CreateClientRequest{Name: "Integration Test Client", Phone: &phone})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, created.ID) //nolint:errcheck

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Integration Test Client" || !got.Active {
		t.Fatalf("unexpected client after create: %+v", got)
	}

	updated, err := service.Update(ctx, created.ID, clients.UpdateClientRequest{Name: "Renamed", Active: false})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" || updated.Active {
		t.Fatalf("unexpected client after update: %+v", updated)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}
