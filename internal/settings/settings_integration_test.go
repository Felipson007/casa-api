//go:build integration

package settings_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/settings"
	"github.com/atlas/atlas-go/internal/settings/postgres"
)

// TestRepository_GetUpdate exercises Service -> postgres.Repository against a real Postgres —
// gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/settings/...
//
// against TEST_DATABASE_URL (never DATABASE_URL). app_settings is a singleton (id boolean PK
// DEFAULT true) — this test upserts the one row directly so it's independent of whatever seed
// data (if any) is already loaded, and restores the pre-test values afterward.
func TestRepository_GetUpdate(t *testing.T) {
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

	if _, err := pool.Exec(ctx, `
		INSERT INTO app_settings (id, company_name, pix_key, logo_path) VALUES (true, 'Seed Co', 'seed@pix.com', '/seed.png')
		ON CONFLICT (id) DO UPDATE SET company_name = EXCLUDED.company_name, pix_key = EXCLUDED.pix_key, logo_path = EXCLUDED.logo_path
	`); err != nil {
		t.Fatalf("seeding app_settings row: %v", err)
	}

	repo := postgres.NewRepository(pool)
	service := settings.NewService(repo)

	got, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CompanyName != "Seed Co" {
		t.Fatalf("expected company name 'Seed Co', got %q", got.CompanyName)
	}

	updated, err := service.Update(ctx, settings.UpdateSettingsRequest{
		CompanyName: "Integration Test Co", PixKey: "integration@pix.com", LogoPath: "/integration.png",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CompanyName != "Integration Test Co" || updated.PixKey != "integration@pix.com" {
		t.Fatalf("expected updated fields round-tripped through Postgres, got %+v", updated)
	}

	reloaded, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("Get (after update): %v", err)
	}
	if reloaded.CompanyName != "Integration Test Co" {
		t.Fatalf("expected the update to persist, got %+v", reloaded)
	}
}
