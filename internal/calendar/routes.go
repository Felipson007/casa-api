package calendar

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the calendar endpoints on mux. Composition happens in cmd/api/main.go.
// Gated by httpx.RequirePerm, module "calendar" — members can't be granted this module yet (it is
// not in the member_permissions CHECK list), which is fine for the single-owner household this app
// targets: the OWNER bypasses every check.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "calendar"
	mux.HandleFunc("GET /api/calendar/events", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/calendar/events", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("PUT /api/calendar/events/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/calendar/events/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
