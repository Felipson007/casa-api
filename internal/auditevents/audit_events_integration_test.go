//go:build integration

package auditevents_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/auditevents"
	auditeventspostgres "github.com/atlas/atlas-go/internal/auditevents/postgres"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
)

// TestRepository_RecordQueryPurge exercises Service -> postgres.Repository against a real Postgres
// — gated behind the "integration" build tag (CI applies internal/migrations first, which creates
// audit_events; see .github/workflows/ci.yml).
//
//	go test -tags=integration ./internal/auditevents/...
func TestRepository_RecordQueryPurge(t *testing.T) {
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

	repo := auditeventspostgres.NewRepository(pool)
	service := auditevents.NewService(repo)

	marker := "integration-auditevents-" + time.Now().Format("20060102150405.000000000")
	defer pool.Exec(ctx, `DELETE FROM audit_events WHERE message = $1`, marker) //nolint:errcheck

	// Old enough that the retention purge should remove it; recent events from other tests stay.
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := service.Record(ctx, eventlog.Event{
		Type:      eventlog.TypeError,
		Severity:  eventlog.SeverityError,
		Path:      "/api/integration-test",
		Message:   marker,
		Detail:    "cause chain",
		Metadata:  map[string]any{"tipo": "internal"},
		CreatedAt: old,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	page, err := service.List(ctx, auditevents.Filter{Search: marker, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 event for the marker, got %d", len(page.Items))
	}
	got := page.Items[0]
	if got.Message != marker || got.Detail != "cause chain" {
		t.Fatalf("unexpected event: %+v", got)
	}
	if page.Total != 1 {
		t.Fatalf("expected total 1, got %d", page.Total)
	}

	single, err := service.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if single.ID != got.ID {
		t.Fatalf("Get returned %s, want %s", single.ID, got.ID)
	}

	stats, err := service.Stats(ctx, 30)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total < 1 {
		t.Fatalf("expected stats total >= 1, got %d", stats.Total)
	}

	n, err := service.PurgeOlderThan(ctx, time.Now().Add(-3*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected the old event to be purged, deleted %d", n)
	}

	after, err := service.List(ctx, auditevents.Filter{Search: marker})
	if err != nil {
		t.Fatalf("List after purge: %v", err)
	}
	if len(after.Items) != 0 {
		t.Fatalf("expected the marker event to be gone, got %d", len(after.Items))
	}
}
