package pandora

import (
	"net/http"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the OmniRoute chat proxy on mux. Composition happens in cmd/api/main.go.
// Gated by httpx.RequirePerm, module "pandora" — the same permission gate as every other module.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "pandora"

	mux.HandleFunc("POST /api/pandora/chat", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ProxyChat)))
}

// RegisterPublicRoutes mounts the external dashboard's chat proxy directly on the root mux —
// bypassing AuthMiddleware, same pattern as passwordreset/auth's public routes (see main.go) —
// because the dashboard is a different origin with no atlas-go login of its own. It is
// gated by a shared token (DASHBOARD_CHAT_TOKEN); when that is unset the route is not mounted at
// all, so an unconfigured deployment has no unauthenticated surface here.
func RegisterPublicRoutes(mux *http.ServeMux, handler *Handler) {
	if !handler.dashboardEnabled() {
		return
	}
	cors := withDashboardCORS(handler.dashboard.AllowedOrigins)
	mux.HandleFunc("POST /api/public/dashboard-chat", cors(httpx.Wrap(handler.DashboardChat)))
	mux.HandleFunc("OPTIONS /api/public/dashboard-chat", cors(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

// withDashboardCORS answers the preflight and sets Access-Control-Allow-Origin ONLY when the
// request's Origin exactly matches one of allowedOrigins (DASHBOARD_CHAT_ALLOWED_ORIGIN, a
// comma-separated list). The dashboard widget builds its request URL from
// window.location.hostname, so the same page is reachable — and therefore sends a different
// Origin — from the LAN IP, the Tailscale name, etc.; a list covers all of them without ever
// reflecting an arbitrary Origin the way the original code did. An empty list, or any other
// Origin, gets no ACAO header. Scoped to dashboard-chat alone: nothing else in the API gets CORS.
func withDashboardCORS(allowedOrigins string) func(http.HandlerFunc) http.HandlerFunc {
	allowed := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Dashboard-Token")
			}
			next(w, r)
		}
	}
}
