package dashboard

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition (composing every other
// module's Service into this one) happens in cmd/api/main.go, same as every other module.
//
// Gated by httpx.RequirePerm module "loans" — the dashboard is fundamentally a loans-centric view
// (KPIs/vigentes are the majority of its content) and every other piece it reads (expenses/income/
// people/assets/finance) is itself already gated by its own module's permission at the point the
// data was created; this endpoint doesn't re-expose anything a caller couldn't already read
// module-by-module. Simpler than inventing a new "dashboard" permission module for one aggregate
// endpoint.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "loans"
	mux.HandleFunc("GET /api/dashboard/kpis", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.KPIs)))
	mux.HandleFunc("GET /api/dashboard/kpis-batch", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.KPIsBatch)))
	mux.HandleFunc("GET /api/dashboard/resumo", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Resumo)))
	mux.HandleFunc("GET /api/dashboard/net-worth", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.NetWorth)))
	mux.HandleFunc("GET /api/dashboard/calendar", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Calendar)))
	// Single-call person detail aggregate (composes people + health + documents + assets services).
	mux.HandleFunc("GET /api/people/{id}/detail", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.PersonDetail)))
}
