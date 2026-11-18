package goals

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the goals endpoints on mux. Composition happens in cmd/api/main.go.
// Gated by httpx.RequirePerm, module "finance" — planning targets are financial data adjacent to
// finance.Balance, so they reuse that permission rather than introducing a new module value.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "finance"
	mux.HandleFunc("GET /api/goals", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/goals", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("PUT /api/goals/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/goals/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
