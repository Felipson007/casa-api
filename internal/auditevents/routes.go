package auditevents

import (
	"net/http"
	"time"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the audit-trail endpoints. Reading/clearing the trail is gated by the
// existing "audit" module permission (owner-only: the module has no member_permissions row and the
// CHECK constraint doesn't grant it), matching Configurações being owner-only. Reporting a
// frontend exception is the one open-to-any-authenticated-user operation, rate-limited so a crash
// loop can't flood the trail.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "audit"
	clientReportLimiter := httpx.NewRateLimiter(30, time.Minute)

	mux.HandleFunc("GET /api/audit-events", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("GET /api/audit-events/stats", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Stats)))
	mux.HandleFunc("GET /api/audit-events/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("DELETE /api/audit-events", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Clear)))
	mux.HandleFunc("POST /api/audit-events/client", httpx.Wrap(httpx.RateLimit(clientReportLimiter, handler.ReportClientError)))
}
