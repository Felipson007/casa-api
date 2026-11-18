package pets

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module "pets" —
// see internal/expenses/routes.go for the reference pattern.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "pets"
	mux.HandleFunc("GET /api/pets", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/pets", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("GET /api/pets/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/pets/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/pets/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
	mux.HandleFunc("GET /api/pets/{petId}/records", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListRecords)))
	mux.HandleFunc("GET /api/pets/{petId}/records/upcoming-vaccines", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListUpcomingVaccines)))
	mux.HandleFunc("POST /api/pets/{petId}/records", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateRecord)))
	mux.HandleFunc("DELETE /api/pets/{petId}/records/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteRecord)))
}
