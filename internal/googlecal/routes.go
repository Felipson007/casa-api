package googlecal

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the Google Calendar endpoints on mux. Composition happens in cmd/api.
// Gated by httpx.RequirePerm module "integrations" — same connection/credential boundary as the
// Drive and iCloud flows. Disconnecting reuses DELETE /api/integrations/external/{type}.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "integrations"
	// Real browser navigations (302s), not fetch calls.
	mux.HandleFunc("GET /api/integrations/google-calendar/connect", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.ConnectGoogleCalendar)))
	mux.HandleFunc("GET /api/integrations/google-calendar/callback", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.GoogleCalendarCallback)))

	mux.HandleFunc("PUT /api/integrations/google-calendar/connection", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Connect)))
	mux.HandleFunc("GET /api/integrations/google-calendar/status", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Status)))
	mux.HandleFunc("GET /api/integrations/google-calendar/calendars", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Calendars)))
	mux.HandleFunc("POST /api/integrations/google-calendar/sync", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.SyncNow)))
}
