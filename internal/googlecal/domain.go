// Package googlecal syncs the household's calendar events with a Google Calendar (two-way) via
// the Calendar API. It is the Google half of the calendar sync; the reconciliation core is shared
// with the iCloud provider in internal/calendarsync.
package googlecal

import (
	"context"
	"time"

	"golang.org/x/oauth2"

	"github.com/atlas/atlas-go/internal/calendarsync"
	"github.com/atlas/atlas-go/internal/integrations"
)

// provider matches calendar.SourceGoogle ("GOOGLE") and the external_provider column.
const provider = "GOOGLE"

type (
	EventData      = calendarsync.EventData
	RemoteCalendar = calendarsync.RemoteCalendar
	RemoteEvent    = calendarsync.RemoteEvent
	Client         = calendarsync.Client
	Store          = calendarsync.Store
	Report         = calendarsync.Report
)

// CredentialSource yields the Google Calendar credentials (ok=false: not connected).
type CredentialSource interface {
	GoogleCalendarCredentials(ctx context.Context) (integrations.GoogleCalendarCredentials, bool, error)
	GoogleCalendarOAuth() *integrations.GoogleCalendarOAuthConfig
}

// Connector owns the OAuth connect/callback + calendar selection and the sync bookkeeping.
type Connector interface {
	BeginGoogleCalendarConnect() (state, authURL string, err error)
	CompleteGoogleCalendarConnect(ctx context.Context, code string) (integrations.ExternalIntegrationDTO, error)
	ConnectGoogleCalendar(ctx context.Context, req integrations.ConnectGoogleCalendarRequest) (integrations.ExternalIntegrationDTO, error)
	RecordGoogleCalendarSync(ctx context.Context, lastSyncedAt time.Time, syncErr string) error
}

// NewClientFunc builds an API client from the OAuth config + refresh token + selected calendar
// (faked in tests).
type NewClientFunc func(ctx context.Context, cfg *oauth2.Config, refreshToken, calendarID string) (Client, error)
