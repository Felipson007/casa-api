package personinfo

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go.
// Every handler is gated by httpx.RequirePerm (D-003), module "people" — this data (RG/CPF/
// diploma/job facts) is exactly as sensitive as the person record it hangs off of, same gate as
// internal/people's own routes. Flat routing (?personId= query filter, personId in the Create/
// CreateBatch request body) matches every other Go module's convention — see the research note in
// domain.go's package comment on why this deviates from Java's one nested controller.
//
// /batch and /equalize are literal segments and take precedence over the {id} wildcard at the same
// position regardless of registration order (Go 1.22 ServeMux) — registered here in the same
// grouped-for-readability style as internal/notifications/routes.go.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "people"
	mux.HandleFunc("GET /api/person-information", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/person-information", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("POST /api/person-information/batch", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateBatch)))
	mux.HandleFunc("POST /api/person-information/equalize", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Equalize)))
	mux.HandleFunc("GET /api/person-information/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/person-information/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/person-information/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
	mux.HandleFunc("GET /api/person-information/{id}/history", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListHistory)))
}
