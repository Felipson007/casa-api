package aiconnectors

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the per-tenant AI connector CRUD on the authenticated mux. The tenant is
// taken from the session (context), never from the request body.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	mux.HandleFunc("GET /api/ai-connectors", httpx.Wrap(handler.List))
	mux.HandleFunc("POST /api/ai-connectors", httpx.Wrap(handler.Create))
	mux.HandleFunc("PUT /api/ai-connectors/{id}", httpx.Wrap(handler.Update))
	mux.HandleFunc("DELETE /api/ai-connectors/{id}", httpx.Wrap(handler.Delete))
}
