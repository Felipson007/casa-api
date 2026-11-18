package icloudcal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/integrations"
)

type fakeClient struct {
	events  map[string]RemoteEvent // by href
	puts    []string
	deletes []string
	putErr  error
}

func newFakeClient(events ...RemoteEvent) *fakeClient {
	c := &fakeClient{events: map[string]RemoteEvent{}}
	for _, e := range events {
		c.events[e.Href] = e
	}
	return c
}

func (c *fakeClient) ListCalendars(context.Context) ([]RemoteCalendar, error) { return nil, nil }

func (c *fakeClient) ListEvents(context.Context, string, time.Time, time.Time) ([]RemoteEvent, error) {
	out := make([]RemoteEvent, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e)
	}
	return out, nil
}

func (c *fakeClient) PutEvent(_ context.Context, path, uid string, data EventData) (RemoteEvent, error) {
	if c.putErr != nil {
		return RemoteEvent{}, c.putErr
	}
	c.puts = append(c.puts, path)
	ev := RemoteEvent{Href: path, UID: uid, ETag: "etag-new", Data: data}
	c.events[path] = ev
	return ev, nil
}

func (c *fakeClient) DeleteEvent(_ context.Context, path string) error {
	c.deletes = append(c.deletes, path)
	delete(c.events, path)
	return nil
}

type fakeStore struct {
	live       []calendar.Event
	deleted    []calendar.Event
	inserted   []calendar.Event
	updated    []calendar.Event
	soft       []uuid.UUID
	cleared    []uuid.UUID
	pushFailed []uuid.UUID
}

func (s *fakeStore) ListLive(context.Context, string, time.Time, time.Time) ([]calendar.Event, error) {
	return s.live, nil
}
func (s *fakeStore) ListDeletedExternal(context.Context, string) ([]calendar.Event, error) {
	return s.deleted, nil
}
func (s *fakeStore) InsertSynced(_ context.Context, e calendar.Event) error {
	s.inserted = append(s.inserted, e)
	return nil
}
func (s *fakeStore) UpdateSynced(_ context.Context, e calendar.Event) error {
	s.updated = append(s.updated, e)
	return nil
}
func (s *fakeStore) SoftDelete(_ context.Context, id uuid.UUID) error {
	s.soft = append(s.soft, id)
	return nil
}
func (s *fakeStore) ClearExternal(_ context.Context, id uuid.UUID) error {
	s.cleared = append(s.cleared, id)
	return nil
}
func (s *fakeStore) MarkPushFailed(_ context.Context, id uuid.UUID, _ string, _ time.Time) error {
	s.pushFailed = append(s.pushFailed, id)
	return nil
}

func runSync(t *testing.T, store *fakeStore, client *fakeClient, now time.Time) (Report, error) {
	t.Helper()
	svc := NewService(Deps{
		Store:     store,
		NewClient: func(context.Context, string, string) (Client, error) { return client, nil },
		Now:       func() time.Time { return now },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return svc.syncAll(context.Background(), client, integrations.ICloudCredentials{CalendarPath: "/cal/"})
}

func TestSync_PushesNewManualEvent(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	store := &fakeStore{live: []calendar.Event{{
		ID: id, Source: calendar.SourceManual, Title: "Dentista", AllDay: true,
		StartDate: day(t, "2026-09-25"), UpdatedAt: now.Add(-time.Hour),
	}}}
	client := newFakeClient()

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Created != 1 {
		t.Fatalf("Created = %d, want 1", rep.Created)
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected one local update, got %d", len(store.updated))
	}
	got := store.updated[0]
	if got.ExternalID == nil || *got.ExternalID != id.String() {
		t.Fatalf("external id = %v, want %s", got.ExternalID, id)
	}
	if got.ExternalProvider == nil || *got.ExternalProvider != provider {
		t.Fatalf("external provider = %v", got.ExternalProvider)
	}
	if got.ETag == nil || *got.ETag != "etag-new" {
		t.Fatalf("etag = %v, want etag-new", got.ETag)
	}
	if got.ExternalSyncedAt == nil {
		t.Fatal("expected external_synced_at to be stamped")
	}
}

func TestSync_InsertsRemoteOnlyEvent(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	remote := RemoteEvent{
		Href: "/cal/r1.ics", UID: "r1", ETag: "e1", Modified: now.Add(-time.Hour),
		Data: EventData{Title: "Do iPhone", AllDay: true, StartDate: day(t, "2026-09-26")},
	}
	store := &fakeStore{}
	client := newFakeClient(remote)

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Pulled != 1 {
		t.Fatalf("Pulled = %d, want 1", rep.Pulled)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected one insert, got %d", len(store.inserted))
	}
	got := store.inserted[0]
	if got.Source != calendar.SourceICloud {
		t.Fatalf("source = %q, want ICLOUD", got.Source)
	}
	if got.ExternalID == nil || *got.ExternalID != "r1" {
		t.Fatalf("external id = %v", got.ExternalID)
	}
}

func TestSync_RemoteDeletionSoftDeletesLocal(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-time.Hour)
	id := uuid.New()
	ext := "r1"
	store := &fakeStore{live: []calendar.Event{{
		ID: id, Source: calendar.SourceICloud, ExternalID: &ext, ETag: strptr("e1"),
		ExternalSyncedAt: &synced, UpdatedAt: synced, ExternalProvider: strptr(provider),
	}}}
	client := newFakeClient()

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Deleted != 1 || len(store.soft) != 1 || store.soft[0] != id {
		t.Fatalf("expected local soft-delete of %s, got rep=%+v soft=%v", id, rep, store.soft)
	}
}

func TestSync_PropagatesLocalDeletion(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	ext, href := "r2", "/cal/r2.ics"
	store := &fakeStore{deleted: []calendar.Event{{
		ID: id, ExternalProvider: strptr(provider), ExternalID: &ext, ExternalHref: &href,
	}}}
	client := newFakeClient()

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Deleted != 1 || len(client.deletes) != 1 || client.deletes[0] != href {
		t.Fatalf("expected remote delete of %s, got rep=%+v deletes=%v", href, rep, client.deletes)
	}
	if len(store.cleared) != 1 || store.cleared[0] != id {
		t.Fatalf("expected mapping cleared for %s, got %v", id, store.cleared)
	}
}

func TestSync_ConflictRemoteNewerPulls(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-3 * time.Hour)
	localUpdated := now.Add(-2 * time.Hour)
	ext := "r1"
	store := &fakeStore{live: []calendar.Event{{
		ID: uuid.New(), Source: calendar.SourceICloud, Title: "Antigo", ExternalID: &ext,
		ETag: strptr("old"), ExternalSyncedAt: &synced, UpdatedAt: localUpdated,
		ExternalProvider: strptr(provider), AllDay: true, StartDate: day(t, "2026-09-20"),
	}}}
	remote := RemoteEvent{
		Href: "/cal/r1.ics", UID: "r1", ETag: "new", Modified: now.Add(-time.Hour),
		Data: EventData{Title: "Novo do iPhone", AllDay: true, StartDate: day(t, "2026-09-21")},
	}
	client := newFakeClient(remote)

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Pulled != 1 || len(store.updated) != 1 {
		t.Fatalf("expected a pull, got rep=%+v updated=%d", rep, len(store.updated))
	}
	got := store.updated[0]
	if got.Title != "Novo do iPhone" {
		t.Fatalf("title = %q, want the remote one", got.Title)
	}
	if got.ETag == nil || *got.ETag != "new" {
		t.Fatalf("etag = %v, want new", got.ETag)
	}
}

func TestSync_ConflictLocalNewerPushes(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-3 * time.Hour)
	localUpdated := now.Add(-1 * time.Hour)
	ext, href := "r1", "/cal/r1.ics"
	store := &fakeStore{live: []calendar.Event{{
		ID: uuid.New(), Source: calendar.SourceManual, Title: "Meu título", ExternalID: &ext,
		ExternalHref: &href, ETag: strptr("old"), ExternalSyncedAt: &synced, UpdatedAt: localUpdated,
		ExternalProvider: strptr(provider), AllDay: true, StartDate: day(t, "2026-09-20"),
	}}}
	remote := RemoteEvent{
		Href: href, UID: "r1", ETag: "new", Modified: now.Add(-2 * time.Hour),
		Data: EventData{Title: "Versão antiga do iPhone", AllDay: true, StartDate: day(t, "2026-09-20")},
	}
	client := newFakeClient(remote)

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Pushed != 1 || len(store.updated) != 1 {
		t.Fatalf("expected a push, got rep=%+v updated=%d", rep, len(store.updated))
	}
	got := store.updated[0]
	if got.Title != "Meu título" {
		t.Fatalf("title = %q, want the local one (local is newer)", got.Title)
	}
	if len(client.puts) != 1 || client.puts[0] != href {
		t.Fatalf("expected a PUT to %s, got %v", href, client.puts)
	}
}

// Report is serialized verbatim by the API and read by the frontend with lower-case keys — a
// missing json tag showed "undefined" in the "Sincronizar agora" toast.
func TestReport_JSONKeysMatchFrontend(t *testing.T) {
	raw, err := json.Marshal(Report{Created: 1, Pushed: 2, Pulled: 3, Deleted: 4})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"created":1,"pushed":2,"pulled":3,"deleted":4,"errors":0}`
	if string(raw) != want {
		t.Fatalf("Report JSON = %s, want %s", raw, want)
	}
}

// A push failure must be visible in the report — otherwise the UI says "tudo em dia" while
// nothing was actually sent.
func TestSync_PushFailureIsReported(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{live: []calendar.Event{{
		ID: uuid.New(), Source: calendar.SourceManual, Title: "Dentista", AllDay: true,
		StartDate: day(t, "2026-09-25"), UpdatedAt: now.Add(-time.Hour),
	}}}
	client := newFakeClient()
	client.putErr = errors.New("403 Forbidden")

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", rep.Errors)
	}
	if rep.Error == "" {
		t.Fatal("expected the first error message to be carried")
	}
	if rep.Created != 0 {
		t.Fatalf("Created = %d, want 0 (the push failed)", rep.Created)
	}
}

// TestSync_PushFailureIsNotRetriedWithinCooldown reproduces the "infinite sync" bug: before the
// fix, syncPush returned on a PutEvent error without persisting anything, so the same event —
// still lacking ExternalID — fell into the "create" branch again on the very next call to Sync,
// retrying the identical failing request forever (e.g. 403 on a read-only/shared iCloud calendar).
func TestSync_PushFailureIsNotRetriedWithinCooldown(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	event := calendar.Event{
		ID: id, Source: calendar.SourceManual, Title: "Dentista", AllDay: true,
		StartDate: day(t, "2026-09-25"), UpdatedAt: now.Add(-time.Hour),
	}
	store := &fakeStore{live: []calendar.Event{event}}
	client := newFakeClient()
	client.putErr = errors.New("403 Forbidden")

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("first syncAll: %v", err)
	}
	if rep.Errors != 1 || len(client.puts) != 0 {
		t.Fatalf("first sync: Errors=%d puts=%d, want Errors=1 puts=0 (the push failed)", rep.Errors, len(client.puts))
	}
	if len(store.pushFailed) != 1 || store.pushFailed[0] != id {
		t.Fatalf("expected the failure to be recorded for %s, got %v", id, store.pushFailed)
	}

	// Second call, moments later: the fake DB now reflects the recorded failure (SyncFailedAt set),
	// same as a real Postgres row would after MarkPushFailed. Without the fix, this event still has
	// no ExternalID and falls into the same "create" branch, calling PutEvent (and failing) again.
	failedAt := now
	event.SyncFailedAt = &failedAt
	store.live = []calendar.Event{event}

	rep2, err := runSync(t, store, client, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second syncAll: %v", err)
	}
	if len(client.puts) != 0 {
		t.Fatalf("expected no new PutEvent call within the retry cooldown, got %d", len(client.puts))
	}
	if rep2.Errors != 0 {
		t.Fatalf("second sync Errors = %d, want 0 (skipped, not retried)", rep2.Errors)
	}
}

func TestSync_NoChanges(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-time.Hour)
	localUpdated := now.Add(-2 * time.Hour)
	ext := "r1"
	store := &fakeStore{live: []calendar.Event{{
		ID: uuid.New(), Source: calendar.SourceICloud, ExternalID: &ext, ETag: strptr("e1"),
		ExternalSyncedAt: &synced, UpdatedAt: localUpdated, ExternalProvider: strptr(provider),
	}}}
	remote := RemoteEvent{Href: "/cal/r1.ics", UID: "r1", ETag: "e1", Modified: localUpdated}
	client := newFakeClient(remote)

	rep, err := runSync(t, store, client, now)
	if err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	if rep != (Report{}) || len(store.updated) != 0 || len(store.inserted) != 0 || len(client.puts) != 0 {
		t.Fatalf("expected a no-op sync, got rep=%+v updated=%d inserted=%d puts=%v",
			rep, len(store.updated), len(store.inserted), client.puts)
	}
}
