package people

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the people + relationships endpoints on mux. No NewModule here (import
// cycle avoidance — see internal/categories/routes.go for the rationale); composition lives in
// cmd/api/main.go.
//
// Every handler is gated by httpx.RequirePerm (D-003), module "people" — this was previously
// missing here (the only module in the whole app without it; found during the full endpoint
// inventory/audit) even though every other module has had it since categories was retrofitted
// (commit 8e0cd13). Without this, any authenticated user (MEMBER included) could read/write
// people and relationships regardless of member_permissions — arguably the most sensitive data
// in the app (family members' identities/relationships), so this was a real gap, not a stylistic
// inconsistency.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "people"
	mux.HandleFunc("GET /api/people", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListPeople)))
	mux.HandleFunc("POST /api/people", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreatePerson)))
	mux.HandleFunc("GET /api/people/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetPerson)))
	mux.HandleFunc("PUT /api/people/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.UpdatePerson)))
	mux.HandleFunc("DELETE /api/people/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeletePerson)))
	mux.HandleFunc("GET /api/relationships", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListRelationships)))
	mux.HandleFunc("POST /api/relationships", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateRelationship)))
	mux.HandleFunc("DELETE /api/relationships/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteRelationship)))
	mux.HandleFunc("GET /api/people/{id}/relationships/derived", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.DeriveFromOrigin)))
}
