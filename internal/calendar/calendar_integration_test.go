//go:build integration

package calendar_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/calendar/postgres"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func boolPtr(b bool) *bool     { return &b }
func strPtr(s string) *string  { return &s }
func day(d int) civildate.Date { return civildate.Of(time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC)) }

// TestRepository_RoundTrip exercises Service -> postgres.Repository against a real Postgres —
// gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/calendar/...
//
// against TEST_DATABASE_URL (never DATABASE_URL). The timed-event path is the one worth guarding:
// it is the only place the module crosses the Postgres `time` type, read back as ::text and
// normalized to "HH:MM".
func TestRepository_RoundTrip(t *testing.T) {
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
	service := calendar.NewService(repo)

	start := day(20)
	created, err := service.Create(ctx, calendar.CreateEventRequest{
		Title:       "Reunião de integração",
		Description: strPtr("round-trip"),
		Location:    strPtr("Sala 1"),
		AllDay:      boolPtr(false),
		StartDate:   &start,
		StartTime:   strPtr("14:00"),
		EndTime:     strPtr("15:30"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM calendar_events WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Source != calendar.SourceManual {
		t.Fatalf("expected MANUAL source, got %q", created.Source)
	}
	if created.AllDay {
		t.Fatal("expected a timed event, got allDay=true")
	}
	// Postgres stores "14:00:00"; the repository normalizes it back to "14:00".
	if created.StartTime == nil || *created.StartTime != "14:00" {
		t.Fatalf("startTime = %v, want 14:00", created.StartTime)
	}
	if created.EndTime == nil || *created.EndTime != "15:30" {
		t.Fatalf("endTime = %v, want 15:30", created.EndTime)
	}
	if created.EndDate != nil {
		t.Fatalf("endDate = %v, want nil (single day)", created.EndDate)
	}

	// The event must be found by the overlap scan.
	inicioMes := civildate.Of(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	fimMes := civildate.Of(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	list, err := service.ListRange(ctx, inicioMes.Time(), fimMes.Time())
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	found := false
	for _, e := range list {
		if e.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created event not returned by ListRange")
	}

	// Editing to an all-day event must clear the times (the CHECK enforces it).
	updated, err := service.Update(ctx, created.ID, calendar.UpdateEventRequest{
		Title:     "Reunião (dia inteiro)",
		AllDay:    boolPtr(true),
		StartDate: &start,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.AllDay || updated.StartTime != nil || updated.EndTime != nil {
		t.Fatalf("all-day update left times behind: allDay=%v start=%v end=%v", updated.AllDay, updated.StartTime, updated.EndTime)
	}

	// Delete is a soft delete: FindByID must stop returning the row.
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("FindByID after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected FindByID to return nil after Delete")
	}
}

func TestRepository_UpdateSyncedStaysReconciled(t *testing.T) {
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
	service := calendar.NewService(repo)

	start := day(21)
	created, err := service.Create(ctx, calendar.CreateEventRequest{
		Title:     "Evento sincronizado",
		AllDay:    boolPtr(true),
		StartDate: &start,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM calendar_events WHERE id = $1`, created.ID) //nolint:errcheck

	ev, err := repo.FindByID(ctx, created.ID)
	if err != nil || ev == nil {
		t.Fatalf("FindByID: %v", err)
	}

	provider := "ICLOUD"
	extID := "uid-reconciled"
	href := "/cal/uid-reconciled.ics"
	etag := "etag-reconciled"
	syncedAt := time.Now().UTC()
	ev.ExternalProvider = &provider
	ev.ExternalID = &extID
	ev.ExternalHref = &href
	ev.ETag = &etag
	ev.ExternalSyncedAt = &syncedAt
	if err := repo.UpdateSynced(ctx, *ev); err != nil {
		t.Fatalf("UpdateSynced: %v", err)
	}

	after, err := repo.FindByID(ctx, created.ID)
	if err != nil || after == nil {
		t.Fatalf("FindByID after sync: %v", err)
	}
	if after.ExternalSyncedAt == nil {
		t.Fatal("external_synced_at was not stored")
	}
	if after.UpdatedAt.After(*after.ExternalSyncedAt) {
		t.Fatalf("updated_at (%s) is after external_synced_at (%s): the row would look locally dirty and be re-pushed every sync", after.UpdatedAt, *after.ExternalSyncedAt)
	}
}
