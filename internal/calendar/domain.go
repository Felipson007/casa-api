// Package calendar is the user-owned calendar module: events the household itself types, as
// opposed to the events the dashboard derives from other modules (loan installments, birthdays,
// document expirations). This slice only stores and serves manual events; the Source field and the
// external_* columns are the hook the Google Calendar / iCloud (CalDAV) sync slices will use to
// match a row to its counterpart on the other side.
package calendar

import (
	"time"

	"github.com/google/uuid"
)

// Source is the calendar_events.source CHECK.
type Source string

const (
	// SourceManual is an event created by the owner in the app. Only this source is written by
	// this slice.
	SourceManual Source = "MANUAL"
	// SourceGoogle / SourceICloud are reserved for the sync slices.
	SourceGoogle Source = "GOOGLE"
	SourceICloud Source = "ICLOUD"
)

// Event mirrors a calendar_events row. StartDate/EndDate are civil days; EndDate nil means the
// event ends the same day it starts. AllDay events carry no times; timed events carry StartTime
// and an optional EndTime as "HH:MM".
type Event struct {
	ID          uuid.UUID
	Title       string
	Description *string
	Location    *string
	AllDay      bool
	StartDate   time.Time
	EndDate     *time.Time
	StartTime   *string
	EndTime     *string
	Source      Source
	// ReadOnly marks an event derived from another module (loan instalment, birthday, document
	// expiration). The sync pushes it to the provider but never pulls edits/deletions back.
	ReadOnly bool

	ExternalProvider   *string
	ExternalID         *string
	ExternalCalendarID *string
	ExternalHref       *string
	ETag               *string
	// ExternalSyncedAt is when this row was last reconciled with the external calendar. A row is
	// "locally dirty" when ExternalSyncedAt is nil or UpdatedAt is after it — see internal/icloudcal.
	ExternalSyncedAt *time.Time
	// SyncError/SyncFailedAt record the last push failure (e.g. 403 on a read-only/shared remote
	// calendar) so calendarsync.Sync stops retrying the same event every run once it has failed.
	// Cleared on the next successful push (see calendarsync.syncPush).
	SyncError    *string
	SyncFailedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}
