// Package icloudcal syncs the household's calendar events with an iCloud Calendar over CalDAV
// (Apple ID + an app-specific password). The Apple half of the calendar sync; the reconciliation
// itself lives in internal/calendarsync, shared with the Google provider.
//
// The package is split so the CalDAV quirks are isolated: caldav_client.go touches the network and
// go-webdav/go-ical, translator.go maps to/from iCalendar, and service.go is orchestration.
package icloudcal

import (
	"context"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/calendarsync"
	"github.com/atlas/atlas-go/internal/integrations"
)

// provider matches calendar.SourceICloud ("ICLOUD") and the external_provider column.
const provider = "ICLOUD"

// The provider-neutral types live in calendarsync; aliases keep this package's API unchanged.
type (
	EventData      = calendarsync.EventData
	RemoteCalendar = calendarsync.RemoteCalendar
	RemoteEvent    = calendarsync.RemoteEvent
	Client         = calendarsync.Client
	Store          = calendarsync.Store
	Report         = calendarsync.Report
)

// CredentialSource yields the iCloud credentials (ok=false: not connected).
type CredentialSource interface {
	ICloudCredentials(ctx context.Context) (integrations.ICloudCredentials, bool, error)
}

// Connector stores the iCloud connection (implemented by integrations.Service).
type Connector interface {
	ConnectICloud(ctx context.Context, req integrations.ConnectICloudRequest) (integrations.ExternalIntegrationDTO, error)
	RecordICloudSync(ctx context.Context, lastSyncedAt time.Time, syncErr string) error
}

// joinPath appends name to a CalDAV collection path, tolerating a missing trailing slash.
func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + name
}
