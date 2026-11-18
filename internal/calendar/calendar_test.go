package calendar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return parsed
}

func cd(t *testing.T, s string) *civildate.Date {
	t.Helper()
	d := civildate.Of(day(t, s))
	return &d
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func TestEventFromRequest_AllDayDefaults(t *testing.T) {
	e, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Consulta",
		StartDate: cd(t, "2026-09-20"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !e.AllDay {
		t.Fatal("allDay should default to true")
	}
	if e.StartTime != nil || e.EndTime != nil {
		t.Fatalf("all-day event must not carry times, got %v/%v", e.StartTime, e.EndTime)
	}
	if e.Source != "" {
		t.Fatalf("source is set by Create, not eventFromRequest; got %q", e.Source)
	}
}

func TestEventFromRequest_AllDayRejectsTime(t *testing.T) {
	_, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Consulta",
		AllDay:    boolPtr(true),
		StartDate: cd(t, "2026-09-20"),
		StartTime: strPtr("09:00"),
	})
	if err == nil {
		t.Fatal("all-day event with a time should be rejected")
	}
}

func TestEventFromRequest_TimedRequiresStartTime(t *testing.T) {
	_, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Reunião",
		AllDay:    boolPtr(false),
		StartDate: cd(t, "2026-09-20"),
	})
	if err == nil {
		t.Fatal("timed event without startTime should be rejected")
	}
}

func TestEventFromRequest_EndTimeAfterStartTime(t *testing.T) {
	_, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Reunião",
		AllDay:    boolPtr(false),
		StartDate: cd(t, "2026-09-20"),
		StartTime: strPtr("15:00"),
		EndTime:   strPtr("14:00"),
	})
	if err == nil {
		t.Fatal("endTime before startTime should be rejected")
	}

	e, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Reunião",
		AllDay:    boolPtr(false),
		StartDate: cd(t, "2026-09-20"),
		StartTime: strPtr("14:00"),
		EndTime:   strPtr("15:30"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.StartTime == nil || *e.StartTime != "14:00" || e.EndTime == nil || *e.EndTime != "15:30" {
		t.Fatalf("times not carried over: %v/%v", e.StartTime, e.EndTime)
	}
}

func TestEventFromRequest_EndDateBeforeStartDate(t *testing.T) {
	_, err := (&Service{}).eventFromRequest(CreateEventRequest{
		Title:     "Viagem",
		StartDate: cd(t, "2026-09-20"),
		EndDate:   cd(t, "2026-09-19"),
	})
	if err == nil {
		t.Fatal("endDate before startDate should be rejected")
	}
}

// An edit must not strip a synced event's identity: the sync slices rely on source + external ids
// surviving a manual edit, or the next push would orphan the external counterpart.
func TestUpdate_PreservesSourceAndExternalMapping(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.events[id] = Event{
		ID: id, Title: "Antigo", AllDay: true, StartDate: day(t, "2026-09-20"),
		Source: SourceICloud, ExternalID: strPtr("ext-1"),
		ExternalCalendarID: strPtr("cal-1"), ExternalProvider: strPtr("ICLOUD"),
		ExternalHref: strPtr("/cal/ext-1.ics"), ETag: strPtr("etag-1"),
	}
	svc := NewService(repo)

	dto, err := svc.Update(context.Background(), id, CreateEventRequest{
		Title:     "Novo título",
		StartDate: cd(t, "2026-09-21"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dto.Title != "Novo título" {
		t.Fatalf("title not updated: %q", dto.Title)
	}
	got := repo.events[id]
	if got.Source != SourceICloud {
		t.Fatalf("source not preserved: %q", got.Source)
	}
	if got.ExternalID == nil || *got.ExternalID != "ext-1" {
		t.Fatalf("external id not preserved: %v", got.ExternalID)
	}
	if got.ExternalHref == nil || *got.ExternalHref != "/cal/ext-1.ics" {
		t.Fatalf("external href not preserved: %v", got.ExternalHref)
	}
	if got.ETag == nil || *got.ETag != "etag-1" {
		t.Fatalf("etag not preserved: %v", got.ETag)
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc := NewService(newFakeRepo())
	if err := svc.Delete(context.Background(), uuid.New()); err == nil {
		t.Fatal("deleting a missing event should return NotFound")
	}
}

func TestListRange_RejectsReversedRange(t *testing.T) {
	svc := NewService(newFakeRepo())
	_, err := svc.ListRange(context.Background(), day(t, "2026-09-30"), day(t, "2026-09-01"))
	if err == nil {
		t.Fatal("to before from should be rejected")
	}
}

// ReplaceDerived keys rows by a deterministic UUID and prunes read-only rows whose source is gone,
// without touching the ids a re-run produces.
func TestReplaceDerived_StableIDsAndPrune(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	start := day(t, "2026-09-25")

	if err := svc.ReplaceDerived(context.Background(), []DerivedEvent{
		{Key: "installment-l1-2026-09-25", Title: "Parcela", StartDate: start},
		{Key: "document-d1", Title: "Documento", StartDate: start},
	}); err != nil {
		t.Fatalf("ReplaceDerived: %v", err)
	}
	if len(repo.events) != 2 {
		t.Fatalf("expected 2 derived rows, got %d", len(repo.events))
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("document-d1"))
	if _, ok := repo.events[id]; !ok {
		t.Fatal("expected the deterministic id for document-d1")
	}

	// A later run without the instalment prunes it but keeps the document.
	if err := svc.ReplaceDerived(context.Background(), []DerivedEvent{
		{Key: "document-d1", Title: "Documento", StartDate: start},
	}); err != nil {
		t.Fatalf("ReplaceDerived (2): %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("expected 1 derived row after prune, got %d", len(repo.events))
	}
}

// --- fake repository ---

type fakeRepo struct {
	events map[uuid.UUID]Event
}

func newFakeRepo() *fakeRepo { return &fakeRepo{events: map[uuid.UUID]Event{}} }

func (f *fakeRepo) ListRange(_ context.Context, from, to time.Time) ([]Event, error) {
	var out []Event
	for _, e := range f.events {
		end := e.StartDate
		if e.EndDate != nil {
			end = *e.EndDate
		}
		if e.StartDate.After(to) || end.Before(from) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (*Event, error) {
	e, ok := f.events[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepo) Create(_ context.Context, e Event) error {
	f.events[e.ID] = e
	return nil
}

func (f *fakeRepo) Update(_ context.Context, e Event) error {
	f.events[e.ID] = e
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.events, id)
	return nil
}

func (f *fakeRepo) UpsertDerived(_ context.Context, e Event) error {
	f.events[e.ID] = e
	return nil
}

func (f *fakeRepo) SoftDeleteReadOnlyNotIn(_ context.Context, keep []uuid.UUID) error {
	keepSet := make(map[uuid.UUID]bool, len(keep))
	for _, id := range keep {
		keepSet[id] = true
	}
	for id, e := range f.events {
		if e.ReadOnly && !keepSet[id] {
			delete(f.events, id)
		}
	}
	return nil
}
