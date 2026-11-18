package openfinance

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints. Connecting/disconnecting is a Configurações →
// Conexões action and is gated by the "integrations" module like every other provider; reading
// and correcting the synced financial data is gated by "finance", like the balances it feeds.
// Disconnecting reuses DELETE /api/integrations/external/PLUGGY (synced data is kept).
func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	perm := httpx.DefaultCheckPerm
	mux.HandleFunc("PUT /api/openfinance/connection", httpx.Wrap(httpx.RequirePerm(perm, "integrations", httpx.PermWrite, h.Connect)))

	mux.HandleFunc("GET /api/openfinance/status", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.Status)))
	mux.HandleFunc("POST /api/openfinance/sync", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermWrite, h.Sync)))
	mux.HandleFunc("GET /api/openfinance/accounts", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.Accounts)))
	mux.HandleFunc("GET /api/openfinance/bills", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.Bills)))
	mux.HandleFunc("GET /api/openfinance/transactions", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.Transactions)))
	mux.HandleFunc("PUT /api/openfinance/transactions/{id}/projection", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermWrite, h.SetProjection)))
	mux.HandleFunc("GET /api/openfinance/investments", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.Investments)))
	mux.HandleFunc("GET /api/openfinance/investments/{id}/history", httpx.Wrap(httpx.RequirePerm(perm, "finance", httpx.PermRead, h.InvestmentHistory)))
}
