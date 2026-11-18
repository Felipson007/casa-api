//go:build integration

package lifemapprefs_test

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/lifemapprefs"
	"github.com/atlas/atlas-go/internal/lifemapprefs/postgres"
)

// TestRepository_SetThenGet_PersistsAndUpserts requires a real app_users row first
// (life_map_preferences.app_user_id is a PK with a foreign key) — this test creates and cleans up
// its own user row directly. Gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/lifemapprefs/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_SetThenGet_PersistsAndUpserts(t *testing.T) {
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

	appUserID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, role) VALUES ($1, $2, 'MEMBER')`,
		appUserID, appUserID.String()+"@test.local"); err != nil {
		t.Fatalf("seeding test app_user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, appUserID)                     //nolint:errcheck
	defer pool.Exec(ctx, `DELETE FROM life_map_preferences WHERE app_user_id = $1`, appUserID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := lifemapprefs.NewService(repo)

	first := []string{"contratos"}
	saved, err := service.Set(ctx, appUserID, lifemapprefs.SetPreferencesRequest{HiddenAreas: first})
	if err != nil {
		t.Fatalf("Set (first): %v", err)
	}
	if !reflect.DeepEqual(saved.HiddenAreas, first) {
		t.Fatalf("Set (first): expected %v, got %v", first, saved.HiddenAreas)
	}
	firstUpdatedAt := saved.UpdatedAt

	// A second Set for the same user must overwrite via ON CONFLICT, not insert a second row.
	second := []string{"contratos", "assinaturas", "garantias"}
	saved, err = service.Set(ctx, appUserID, lifemapprefs.SetPreferencesRequest{HiddenAreas: second})
	if err != nil {
		t.Fatalf("Set (second): %v", err)
	}
	if !reflect.DeepEqual(saved.HiddenAreas, second) {
		t.Fatalf("Set (second, overwrite): expected %v, got %v", second, saved.HiddenAreas)
	}
	if !saved.UpdatedAt.After(firstUpdatedAt) {
		t.Fatalf("expected updated_at to advance on upsert: first=%v second=%v", firstUpdatedAt, saved.UpdatedAt)
	}

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got.HiddenAreas, second) {
		t.Fatalf("Get (after upsert): expected %v, got %v", second, got.HiddenAreas)
	}
}

func TestRepository_Get_EmptyForUserWithoutRow(t *testing.T) {
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

	appUserID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, role) VALUES ($1, $2, 'MEMBER')`,
		appUserID, appUserID.String()+"@test.local"); err != nil {
		t.Fatalf("seeding test app_user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, appUserID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := lifemapprefs.NewService(repo)

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("expected no error for a user with no saved preferences, got %v", err)
	}
	if got.HiddenAreas == nil {
		t.Fatal("expected an empty slice, not nil")
	}
	if len(got.HiddenAreas) != 0 {
		t.Fatalf("expected no hidden areas, got %v", got.HiddenAreas)
	}
	if !got.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a user with no saved preferences, got %v", got.UpdatedAt)
	}
}

// TestRepository_Get_EmptyHiddenAreasIsNeverNil guards the JSON-null-breaks-frontend rule (see
// docs/code-quality.md) at the repository level, not just in the Service's own defensive default.
func TestRepository_Get_EmptyHiddenAreasIsNeverNil(t *testing.T) {
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

	appUserID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, role) VALUES ($1, $2, 'MEMBER')`,
		appUserID, appUserID.String()+"@test.local"); err != nil {
		t.Fatalf("seeding test app_user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, appUserID)                     //nolint:errcheck
	defer pool.Exec(ctx, `DELETE FROM life_map_preferences WHERE app_user_id = $1`, appUserID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := lifemapprefs.NewService(repo)

	saved, err := service.Set(ctx, appUserID, lifemapprefs.SetPreferencesRequest{HiddenAreas: []string{}})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if saved.HiddenAreas == nil {
		t.Fatal("expected Set to return an empty slice, not nil")
	}

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HiddenAreas == nil {
		t.Fatal("expected Get to return an empty slice, not nil")
	}
}
