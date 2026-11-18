//go:build integration

package dashboardlayout_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/dashboardlayout"
	"github.com/atlas/atlas-go/internal/dashboardlayout/postgres"
)

// assertLayoutEquivalent compares two JSON payloads semantically, not byte-for-byte — Postgres's
// jsonb normalizes key order/whitespace on the round-trip (e.g. alphabetizes object keys), so a
// literal string comparison against what was sent would fail even though the data is identical.
func assertLayoutEquivalent(t *testing.T, label string, got, want []byte) {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("%s: got is not valid JSON: %v (%s)", label, err, got)
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("%s: want is not valid JSON: %v (%s)", label, err, want)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Fatalf("%s: expected %s, got %s", label, want, got)
	}
}

// TestRepository_SetThenGet_PersistsAndUpserts requires a real app_users row first
// (dashboard_layouts.app_user_id is a PK with a foreign key) — this test creates and cleans up
// its own user row directly. Gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/dashboardlayout/...
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
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, appUserID)                  //nolint:errcheck
	defer pool.Exec(ctx, `DELETE FROM dashboard_layouts WHERE app_user_id = $1`, appUserID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := dashboardlayout.NewService(repo)

	first := []byte(`[{"i":"life-map","x":0,"y":0,"w":8,"h":4}]`)
	saved, err := service.Set(ctx, appUserID, dashboardlayout.SetLayoutRequest{Layout: first})
	if err != nil {
		t.Fatalf("Set (first): %v", err)
	}
	assertLayoutEquivalent(t, "Set (first)", saved.Layout, first)
	firstUpdatedAt := saved.UpdatedAt

	// A second Set for the same user must overwrite via ON CONFLICT, not insert a second row.
	second := []byte(`[{"i":"life-map","x":4,"y":0,"w":8,"h":4},{"i":"inbox","x":0,"y":4,"w":4,"h":3}]`)
	saved, err = service.Set(ctx, appUserID, dashboardlayout.SetLayoutRequest{Layout: second})
	if err != nil {
		t.Fatalf("Set (second): %v", err)
	}
	assertLayoutEquivalent(t, "Set (second, overwrite)", saved.Layout, second)
	if !saved.UpdatedAt.After(firstUpdatedAt) {
		t.Fatalf("expected updated_at to advance on upsert: first=%v second=%v", firstUpdatedAt, saved.UpdatedAt)
	}

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertLayoutEquivalent(t, "Get (after upsert)", got.Layout, second)
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
	service := dashboardlayout.NewService(repo)

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("expected no error for a user with no saved layout, got %v", err)
	}
	if got.Layout != nil {
		t.Fatalf("expected a nil (null) layout, got %s", got.Layout)
	}
	if !got.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a user with no saved layout, got %v", got.UpdatedAt)
	}
}
