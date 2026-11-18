//go:build integration

package health_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/health/postgres"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// TestRepository_MarkerCreateGetUpdateDelete and TestRepository_RecordCreateGetUpdateDelete
// exercise Service -> postgres.Repository against a real Postgres — gated behind the
// "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/health/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_MarkerCreateGetUpdateDelete(t *testing.T) {
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
	service := health.NewService(repo)

	unit := "mg/dL"
	created, err := service.CreateMarker(ctx, health.CreateMarkerCatalogRequest{Name: "Integration Test Marker", Unit: &unit})
	if err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM health_marker_catalog WHERE id = $1`, created.ID) //nolint:errcheck

	got, err := service.GetMarker(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMarker: %v", err)
	}
	if got.Unit == nil || *got.Unit != "mg/dL" {
		t.Fatalf("expected unit mg/dL, got %v", got.Unit)
	}

	if err := service.DeleteMarker(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMarker: %v", err)
	}
	if _, err := service.GetMarker(ctx, created.ID); err == nil {
		t.Fatal("expected GetMarker to fail after Delete")
	}
}

// TestRepository_RecordCreateGetUpdateDelete requires a real person row first (health_records.person_id
// is NOT NULL with a foreign key) — this test creates and cleans up its own person row directly.
func TestRepository_RecordCreateGetUpdateDelete(t *testing.T) {
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

	var personID string
	if err := pool.QueryRow(ctx, `INSERT INTO people (name) VALUES ('Health Test Person') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("seeding test person: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM people WHERE id = $1`, personID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := health.NewService(repo)

	parsedPersonID, err := uuid.Parse(personID)
	if err != nil {
		t.Fatalf("parsing seeded person id: %v", err)
	}

	created, err := service.CreateRecord(ctx, health.CreateRecordRequest{
		PersonID:   parsedPersonID,
		Type:       health.RecordTypeLabResult,
		RecordDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM health_records WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Status != health.StatusDone {
		t.Fatalf("expected DONE status, got %v", created.Status)
	}

	got, err := service.GetRecord(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if got.Type != health.RecordTypeLabResult {
		t.Fatalf("expected type LAB_RESULT, got %q", got.Type)
	}

	updated, err := service.UpdateRecord(ctx, created.ID, health.UpdateRecordRequest{
		Type:       health.RecordTypeLabResult,
		RecordDate: created.RecordDate,
		Status:     health.StatusCancelled,
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if updated.Status != health.StatusCancelled {
		t.Fatalf("expected CANCELLED status after update, got %v", updated.Status)
	}

	if err := service.DeleteRecord(ctx, created.ID); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
}
