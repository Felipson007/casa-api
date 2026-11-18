package icloudcal

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the iCloud Calendar endpoints on mux. Composition happens in cmd/api.
// Gated by httpx.RequirePerm module "integrations" — this is a connection/credential operation,
// same as the Google Drive and Pluggy connect flows. Disconnecting reuses the generic
// DELETE /api/integrations/external/{type} route.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "integrations"
	mux.HandleFunc("PUT /api/integrations/icloud-calendar/connection", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Connect)))
	mux.HandleFunc("GET /api/integrations/icloud-calendar/status", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Status)))
	mux.HandleFunc("GET /api/integrations/icloud-calendar/calendars", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Calendars)))
	mux.HandleFunc("POST /api/integrations/icloud-calendar/sync", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.SyncNow)))
	// Diagnostics against the real CalDAV server (structural, no names/ids) — the counterpart of
	// cmd/pluggy-probe, reachable in the deployed image where the probe binary isn't shipped.
	mux.HandleFunc("POST /api/integrations/icloud-calendar/probe", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Probe)))
}
