package health

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module "health" —
// see internal/expenses/routes.go for the reference pattern.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "health"

	mux.HandleFunc("GET /api/health/markers", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListMarkers)))
	mux.HandleFunc("POST /api/health/markers", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateMarker)))
	mux.HandleFunc("GET /api/health/markers/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetMarker)))
	mux.HandleFunc("PUT /api/health/markers/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.UpdateMarker)))
	mux.HandleFunc("DELETE /api/health/markers/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteMarker)))

	mux.HandleFunc("GET /api/health/records", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListRecords)))
	mux.HandleFunc("POST /api/health/records", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateRecord)))
	mux.HandleFunc("GET /api/health/records/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetRecord)))
	mux.HandleFunc("PUT /api/health/records/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.UpdateRecord)))
	mux.HandleFunc("DELETE /api/health/records/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteRecord)))
}
