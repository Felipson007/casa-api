package auditevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
)

type fakeRepository struct {
	inserted   []Event
	items      []Event
	total      int
	purged     int64
	cleared    int64
	lastFilter Filter
	stats      Stats
}

func (f *fakeRepository) Insert(_ context.Context, ev Event) error {
	f.inserted = append(f.inserted, ev)
	return nil
}

func (f *fakeRepository) List(_ context.Context, filter Filter) ([]Event, error) {
	f.lastFilter = filter
	return f.items, nil
}

func (f *fakeRepository) Count(_ context.Context, _ Filter) (int, error) {
	if f.total != 0 {
		return f.total, nil
	}
	return len(f.items), nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Event, error) {
	for i := range f.items {
		if f.items[i].ID == id {
			return &f.items[i], nil
		}
	}
	return nil, nil
}

func (f *fakeRepository) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return f.purged, nil
}

func (f *fakeRepository) DeleteAll(_ context.Context) (int64, error) {
	return f.cleared, nil
}

func (f *fakeRepository) Stats(_ context.Context, _ time.Time) (Stats, error) {
	return f.stats, nil
}

func TestService_Record_MapsEventlogFields(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo)

	userID := uuid.New()
	status := 500
	duration := 42
	in := eventlog.Event{
		Type:       eventlog.TypeError,
		Severity:   eventlog.SeverityError,
		UserID:     &userID,
		UserEmail:  "owner@example.com",
		IP:         "10.0.0.1",
		Method:     "GET",
		Path:       "/api/loans",
		StatusCode: status,
		Message:    "boom",
		Detail:     "cause chain",
		Metadata:   map[string]any{"tipo": "internal"},
		DurationMS: duration,
	}

	if err := service.Record(context.Background(), in); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(repo.inserted))
	}
	got := repo.inserted[0]
	if got.Type != string(eventlog.TypeError) || got.Severity != string(eventlog.SeverityError) {
		t.Fatalf("unexpected type/severity: %q/%q", got.Type, got.Severity)
	}
	if got.UserID == nil || *got.UserID != userID {
		t.Fatalf("expected user id %s, got %v", userID, got.UserID)
	}
	if got.StatusCode == nil || *got.StatusCode != status {
		t.Fatalf("expected status code %d, got %v", status, got.StatusCode)
	}
	if got.DurationMS == nil || *got.DurationMS != duration {
		t.Fatalf("expected duration %d, got %v", duration, got.DurationMS)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be filled in")
	}
}

func TestService_List_NormalizesPagination(t *testing.T) {
	repo := &fakeRepository{items: make([]Event, 3)}
	service := NewService(repo)

	result, err := service.List(context.Background(), Filter{Limit: 5000, Offset: -10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if repo.lastFilter.Limit != MaxPageSize {
		t.Fatalf("expected limit clamped to %d, got %d", MaxPageSize, repo.lastFilter.Limit)
	}
	if repo.lastFilter.Offset != 0 {
		t.Fatalf("expected offset clamped to 0, got %d", repo.lastFilter.Offset)
	}
	if result.Size != MaxPageSize {
		t.Fatalf("expected result size %d, got %d", MaxPageSize, result.Size)
	}
	if result.TotalPages != 1 {
		t.Fatalf("expected 1 page for 3 items at max size, got %d", result.TotalPages)
	}
}

func TestService_Stats_ClampsDays(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo)

	stats, err := service.Stats(context.Background(), 1000)
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if stats.WindowDays != MaxStatsDays {
		t.Fatalf("expected window clamped to %d, got %d", MaxStatsDays, stats.WindowDays)
	}

	stats, err = service.Stats(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if stats.WindowDays != DefaultStatsDays {
		t.Fatalf("expected default window %d, got %d", DefaultStatsDays, stats.WindowDays)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	service := NewService(&fakeRepository{})

	_, err := service.Get(context.Background(), uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestNormalizeFilter_TruncatesSearch(t *testing.T) {
	long := make([]byte, maxSearchLen+50)
	for i := range long {
		long[i] = 'a'
	}
	filter := normalizeFilter(Filter{Search: string(long)})
	if len(filter.Search) != maxSearchLen {
		t.Fatalf("expected search truncated to %d, got %d", maxSearchLen, len(filter.Search))
	}
}
