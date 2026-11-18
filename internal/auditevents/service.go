package auditevents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
)

const (
	// DefaultPageSize / MaxPageSize bound the list endpoint: the audit trail can hold thousands of
	// rows and the UI table only ever shows a page or two.
	DefaultPageSize = 25
	MaxPageSize     = 100
	// maxSearchLen caps the free-text search term before it reaches ILIKE.
	maxSearchLen = 120
	// DefaultStatsDays is the chart window when the client doesn't ask for one.
	DefaultStatsDays = 7
	// MaxStatsDays bounds the chart window so a hostile/buggy client can't force a giant scan.
	MaxStatsDays = 90
)

// Service holds the business rules for the audit trail.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Record persists one event produced by the platform recorder (httpx middleware or auth).
func (s *Service) Record(ctx context.Context, ev eventlog.Event) error {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	e := Event{
		Type:      string(ev.Type),
		Severity:  string(ev.Severity),
		UserID:    ev.UserID,
		UserEmail: ev.UserEmail,
		TenantID:  ev.TenantID,
		IP:        ev.IP,
		UserAgent: ev.UserAgent,
		Method:    ev.Method,
		Path:      ev.Path,
		Message:   ev.Message,
		Detail:    ev.Detail,
		Metadata:  ev.Metadata,
		CreatedAt: ev.CreatedAt,
	}
	if ev.StatusCode != 0 {
		e.StatusCode = &ev.StatusCode
	}
	if ev.DurationMS != 0 {
		e.DurationMS = &ev.DurationMS
	}
	e.RequestID = ev.RequestID
	if err := s.repo.Insert(ctx, e); err != nil {
		return fmt.Errorf("auditevents: inserting: %w", err)
	}
	return nil
}

// ListResult is a page of events plus the totals the UI paginator needs.
type ListResult struct {
	Items      []Event
	Total      int
	Page       int
	Size       int
	TotalPages int
}

// List returns one page of events, newest first, matching filter.
func (s *Service) List(ctx context.Context, filter Filter) (ListResult, error) {
	filter = normalizeFilter(filter)

	items, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, fmt.Errorf("auditevents: listing: %w", err)
	}
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return ListResult{}, fmt.Errorf("auditevents: counting: %w", err)
	}

	size := filter.Limit
	if size <= 0 {
		size = DefaultPageSize
	}
	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}
	return ListResult{
		Items:      items,
		Total:      total,
		Page:       filter.Offset / size,
		Size:       size,
		TotalPages: totalPages,
	}, nil
}

// Get returns a single event or apperr.NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Event, error) {
	ev, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Event{}, fmt.Errorf("auditevents: looking up: %w", err)
	}
	if ev == nil {
		return Event{}, apperr.NotFound("audit event not found")
	}
	return *ev, nil
}

// Stats returns the aggregate view for the last days (clamped to a sane window).
func (s *Service) Stats(ctx context.Context, days int) (Stats, error) {
	if days <= 0 {
		days = DefaultStatsDays
	}
	if days > MaxStatsDays {
		days = MaxStatsDays
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	stats, err := s.repo.Stats(ctx, since)
	if err != nil {
		return Stats{}, fmt.Errorf("auditevents: computing stats: %w", err)
	}
	if stats.WindowDays == 0 {
		stats.WindowDays = days
	}
	return stats, nil
}

// PurgeOlderThan deletes every event older than cutoff — the retention job that keeps the table
// from growing without bound.
func (s *Service) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.repo.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("auditevents: purging: %w", err)
	}
	return n, nil
}

// Clear deletes every event (the owner's "limpar auditoria" action).
func (s *Service) Clear(ctx context.Context) (int64, error) {
	n, err := s.repo.DeleteAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("auditevents: clearing: %w", err)
	}
	return n, nil
}

func normalizeFilter(f Filter) Filter {
	f.Type = strings.TrimSpace(f.Type)
	f.Severity = strings.TrimSpace(f.Severity)
	f.Search = strings.TrimSpace(f.Search)
	if len(f.Search) > maxSearchLen {
		f.Search = f.Search[:maxSearchLen]
	}
	if f.Limit <= 0 {
		f.Limit = DefaultPageSize
	}
	if f.Limit > MaxPageSize {
		f.Limit = MaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}
