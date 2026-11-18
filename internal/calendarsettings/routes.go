package calendarsettings

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the calendar sync target endpoints. Gated by module "calendar" (same as
// the events module; OWNER bypasses).
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "calendar"
	mux.HandleFunc("GET /api/calendar/sync-target", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/calendar/sync-target", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Set)))
}
