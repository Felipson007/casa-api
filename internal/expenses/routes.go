package expenses

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005) — see internal/categories/routes.go for why.
//
// Every handler is gated by httpx.RequirePerm (D-003): OWNER always passes, MEMBER needs a
// matching member_permissions row for module "expenses". Uses httpx.DefaultCheckPerm, set once by
// the composition root, so this module doesn't need the check function threaded through
// RegisterRoutes' signature.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "expenses"
	mux.HandleFunc("GET /api/expenses", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/expenses", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	// Literal segments below take precedence over the {id} wildcard at the same position (Go 1.22
	// ServeMux) — order of registration doesn't matter, but they must still be registered.
	mux.HandleFunc("GET /api/expenses/monthly-summary", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.MonthlySummary)))
	mux.HandleFunc("GET /api/expenses/annual-summary", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.AnnualSummary)))
	mux.HandleFunc("GET /api/expenses/subscriptions", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Subscriptions)))
	mux.HandleFunc("POST /api/expenses/autocategorize", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Autocategorize)))
	mux.HandleFunc("GET /api/expenses/uncategorized", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Uncategorized)))
	mux.HandleFunc("GET /api/expenses/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/expenses/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/expenses/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
}
