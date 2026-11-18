package auditevents

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/platform/eventlog"
)

type fakeStore struct {
	mu        sync.Mutex
	persisted []eventlog.Event
	purges    int
}

func (f *fakeStore) Record(_ context.Context, ev eventlog.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persisted = append(f.persisted, ev)
	return nil
}

func (f *fakeStore) PurgeOlderThan(_ context.Context, _ time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges++
	return 0, nil
}

func (f *fakeStore) snapshot() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.persisted), f.purges
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecorder_PersistsAndPurges(t *testing.T) {
	store := &fakeStore{}
	rec := newRecorder(store, testLogger(), 72*time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec.Start(ctx)
	defer rec.Close()

	rec.Record(ctx, eventlog.Event{Type: eventlog.TypeError, Message: "boom"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := store.snapshot(); n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	n, purges := store.snapshot()
	if n != 1 {
		t.Fatalf("expected 1 persisted event, got %d", n)
	}
	if purges == 0 {
		t.Fatal("expected the startup retention purge to run")
	}
}

func TestRecorder_DisablesPurgeWhenRetentionZero(t *testing.T) {
	store := &fakeStore{}
	rec := newRecorder(store, testLogger(), 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	rec.Close()

	if _, purges := store.snapshot(); purges != 0 {
		t.Fatalf("expected no purge when retention is disabled, got %d", purges)
	}
}

func TestRecorder_CloseIsIdempotent(t *testing.T) {
	store := &fakeStore{}
	rec := newRecorder(store, testLogger(), 0)
	rec.Start(context.Background())
	rec.Close()
	rec.Close() // must not panic (closeOnce)
}
