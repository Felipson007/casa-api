package jobexperience

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go.
// Every handler is gated by httpx.RequirePerm, module "people" — a person's salary history is
// exactly as sensitive as the person record it hangs off of, same gate as internal/personinfo.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "people"
	mux.HandleFunc("GET /api/job-experiences", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/job-experiences", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("PUT /api/job-experiences/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/job-experiences/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
