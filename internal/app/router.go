// Package app assembles the global middleware chain around the mux that each module's
// RegisterRoutes populates (docs/architecture-plan.md directory tree). It knows nothing about
// individual modules — cmd/api/main.go builds each module and calls its RegisterRoutes on the mux
// passed in here, keeping this package free of per-module imports.
package app

import (
	"log/slog"
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// NewRouter wraps mux with the global middleware chain: panic recovery (outermost, so it can
// catch a panic anywhere below it), then access logging, then a cross-site-mutation guard
// (CSRF defense-in-depth over SameSite=Lax) — see docs/architecture-plan.md section 12.
// selfOrigin is APP_FRONTEND_ORIGIN.
func NewRouter(mux *http.ServeMux, logger *slog.Logger, selfOrigin string) http.Handler {
	handler := http.Handler(mux)
	handler = httpx.RejectCrossSiteMutations(selfOrigin)(handler)
	handler = httpx.AccessLog(logger)(handler)
	handler = httpx.Recover(handler)
	return handler
}
