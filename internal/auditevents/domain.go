// Package auditevents is the audit-trail read/write module: it persists the events defined in
// internal/platform/eventlog (logins, application errors, panics, frontend exceptions) into the
// control-plane audit_events table and serves them to the Configurações → Auditoria screen.
//
// It is deliberately separate from internal/audit, which is the unrelated row-edit trail
// (audit_logs, keyed by table_name/row_id). This package owns the security/exception trail.
package auditevents

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Event mirrors one audit_events row.
type Event struct {
	ID         uuid.UUID
	Type       string
	Severity   string
	UserID     *uuid.UUID
	UserEmail  string
	TenantID   *uuid.UUID
	IP         string
	UserAgent  string
	Method     string
	Path       string
	StatusCode *int
	RequestID  string
	Message    string
	Detail     string
	Metadata   map[string]any
	DurationMS *int
	CreatedAt  time.Time
}

// Filter narrows a List/Count query. Zero values mean "no constraint".
type Filter struct {
	Type     string
	Severity string
	Search   string
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

// DayCount is one bucket of the errors-over-time chart.
type DayCount struct {
	Day    string
	Total  int
	Errors int
}

// TypeCount is one slice of the events-by-type chart.
type TypeCount struct {
	Type  string
	Count int
}

// Stats is the aggregate view backing the Auditoria dashboard cards and charts.
type Stats struct {
	WindowDays   int
	Total        int
	Errors       int
	Warnings     int
	Logins       int
	FailedLogins int
	ByDay        []DayCount
	ByType       []TypeCount
	RecentLogins []Event
}

// Repository is the audit_events storage port, implemented by internal/auditevents/postgres.
type Repository interface {
	Insert(ctx context.Context, ev Event) error
	List(ctx context.Context, filter Filter) ([]Event, error)
	Count(ctx context.Context, filter Filter) (int, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Event, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteAll(ctx context.Context) (int64, error)
	Stats(ctx context.Context, since time.Time) (Stats, error)
}
