package clients

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition (postgres.NewRepository ->
// NewService -> NewHandler) happens in cmd/api/main.go, not here — see
// internal/categories/routes.go for why (import-cycle avoidance); cmd/api/main.go is Hermes-owned
// per decisions/DECISIONS.md D-005, so this module only requests wiring via a review note.
//
// Every handler is gated by httpx.RequirePerm (D-003) — retrofitted after
// internal/platform/httpx/authz.go landed; see internal/expenses/routes.go for the reference
// pattern this follows.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "clients"
	mux.HandleFunc("GET /api/clients", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/clients", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("GET /api/clients/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/clients/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/clients/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
