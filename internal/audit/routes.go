package audit

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per the audit report, docs/audit-20260809.md). Every handler is gated by
// httpx.RequirePerm (D-003), module "audit" — see internal/expenses/routes.go for the reference
// pattern.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "audit"

	mux.HandleFunc("GET /api/audit-logs", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/audit-logs", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("GET /api/audit-logs/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
}
