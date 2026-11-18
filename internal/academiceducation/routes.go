package academiceducation

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go.
// Every handler is gated by httpx.RequirePerm, module "people" — same gate as
// internal/jobexperience (a person's education history is exactly as sensitive as the person
// record it hangs off of).
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "people"
	mux.HandleFunc("GET /api/academic-educations", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/academic-educations", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("PUT /api/academic-educations/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/academic-educations/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
