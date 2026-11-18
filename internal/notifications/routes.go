package notifications

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module
// "notifications" — see internal/expenses/routes.go for the reference pattern.
//
// The /configuration routes are registered before /{id} so the literal segment takes precedence
// over the wildcard — Go 1.22's ServeMux prefers the more specific pattern regardless of
// registration order, but keeping them visually grouped together here for readability.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "notifications"

	mux.HandleFunc("GET /api/notifications/configuration", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetConfiguration)))
	mux.HandleFunc("PUT /api/notifications/configuration", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.UpdateConfiguration)))

	mux.HandleFunc("GET /api/notifications", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/notifications", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("DELETE /api/notifications", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteAll)))
	// One UPDATE statement instead of the frontend firing one PUT per unread notification.
	mux.HandleFunc("POST /api/notifications/mark-all-read", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.MarkAllRead)))
	mux.HandleFunc("GET /api/notifications/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/notifications/{id}/read", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.MarkRead)))
	mux.HandleFunc("DELETE /api/notifications/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
