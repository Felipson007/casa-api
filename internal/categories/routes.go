package categories

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's 3 endpoints on mux. Note there is no module.go /
// NewModule(pool) here building the postgres repository directly, as the original sketch in
// docs/architecture-plan.md's directory tree suggested — postgres.Repository must import this
// package for the Category/Repository types it implements, so a module.go inside this package
// that also imported postgres would create an import cycle (categories -> postgres -> categories).
// The composition root — building postgres.NewRepository, then NewService, then NewHandler — lives
// in cmd/api/main.go instead, which is free to import both packages since neither of them imports
// cmd/api. This is a Go-idiom correction to the plan's sketch, not a design change.
//
// Every handler is gated by httpx.RequirePerm (D-003): OWNER always passes, MEMBER needs a
// matching member_permissions row for module "categories". Uses httpx.DefaultCheckPerm, set once
// by the composition root, so this module doesn't need the check function threaded through
// RegisterRoutes' signature. Mirrors internal/expenses/routes.go.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "categories"
	mux.HandleFunc("GET /api/categories", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/categories", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("DELETE /api/categories/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
