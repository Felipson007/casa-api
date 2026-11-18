package settings

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the settings endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Gated by httpx.RequirePerm (D-003), module "settings".
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "settings"

	mux.HandleFunc("GET /api/settings", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/settings", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
}
