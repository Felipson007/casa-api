package finance

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module "finance"
// — see internal/expenses/routes.go for the reference pattern. No GET /{id} route exists — matches
// the real Java SaldoFinanceiroController 1:1 (List/Create/Update/Delete + the history sub-route).
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "finance"
	mux.HandleFunc("GET /api/finance/balances", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/finance/balances", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("PUT /api/finance/balances/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/finance/balances/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
	mux.HandleFunc("GET /api/finance/balances/{id}/history", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListHistory)))
}
