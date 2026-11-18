package lifemapprefs

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go.
// Every handler is gated by httpx.RequirePerm (D-003), module "lifemapprefs" — see
// internal/expenses/routes.go for the reference pattern.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "lifemapprefs"
	mux.HandleFunc("GET /api/life-map/preferences", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/life-map/preferences", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Set)))
}
