// Package calendarsync holds the provider-neutral reconciliation core shared by the iCloud
// (CalDAV) and Google Calendar integrations. Providers differ only in how they talk to the remote
// server and translate events; the matching/dirty-tracking/last-write-wins logic lives here.
package calendarsync

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/calendar"
)

// EventData is the provider-neutral shape of a calendar event. StartDate/EndDate are civil days;
// StartTime/EndTime are "HH:MM" and only set when AllDay is false.
type EventData struct {
	Title       string
	Description *string
	Location    *string
	AllDay      bool
	StartDate   time.Time
	EndDate     *time.Time
	StartTime   *string
	EndTime     *string
}

// RemoteCalendar is one calendar collection the account exposes.
type RemoteCalendar struct {
	Path string // provider-specific id/path, stored as external_calendar_id
	Name string
}

// RemoteEvent is one event as the provider sees it.
type RemoteEvent struct {
	Href     string // provider-specific resource id/path, stored as external_href
	UID      string // stable id, stored as external_id
	ETag     string // change token (CalDAV ETag / Google updated)
	Modified time.Time
	Data     EventData
}

// Client is the remote calendar slice the sync needs.
type Client interface {
	ListCalendars(ctx context.Context) ([]RemoteCalendar, error)
	ListEvents(ctx context.Context, calendarID string, from, to time.Time) ([]RemoteEvent, error)
	PutEvent(ctx context.Context, href, uid string, data EventData) (RemoteEvent, error)
	DeleteEvent(ctx context.Context, href string) error
}

// Store persists calendar rows for the sync (implemented by calendar/postgres.Repository).
type Store interface {
	// ListLive returns live events that belong to this provider: never-synced (external_provider
	// NULL) or already mapped to it. Rows mapped to a different provider are excluded, so switching
	// the sync target never makes one provider delete another provider's events.
	ListLive(ctx context.Context, provider string, from, to time.Time) ([]calendar.Event, error)
	ListDeletedExternal(ctx context.Context, provider string) ([]calendar.Event, error)
	InsertSynced(ctx context.Context, e calendar.Event) error
	UpdateSynced(ctx context.Context, e calendar.Event) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
	ClearExternal(ctx context.Context, id uuid.UUID) error
	// MarkPushFailed records a push failure (e.g. 403 on a read-only/shared remote calendar) so the
	// next Sync run can skip retrying this exact event until pushRetryGap has passed — a permanent
	// error otherwise repeated identically on every single run, forever.
	MarkPushFailed(ctx context.Context, id uuid.UUID, cause string, failedAt time.Time) error
}

// pushRetryGap bounds how often Sync retries an event that just failed to push. Without it, a
// permanent error (403 on a shared/read-only remote calendar) is retried identically on every
// call to Sync — including the user's manual "sync now" button, which has no cooldown of its own.
const pushRetryGap = time.Hour

// Params configures one reconciliation.
type Params struct {
	Client       Client
	Store        Store
	Provider     string                  // external_provider value, e.g. "ICLOUD"/"GOOGLE"
	Source       calendar.Source         // source for pulled-in events, e.g. SourceICloud/SourceGoogle
	CalendarID   string                  // selected remote calendar (external_calendar_id)
	HrefFor      func(uid string) string // maps a UID to the remote resource id for a fresh create
	WindowPast   time.Duration
	WindowFuture time.Duration
	Now          time.Time
	Logf         func(msg string, err error)
}
