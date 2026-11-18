package tenancy

import (
	"net/http"
	"time"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// signupRateLimit / signupRateWindow throttle POST /api/auth/signup per client IP. Signup is rare
// and creates a whole schema, so the window is generous; this only blunts a scripted loop.
const (
	signupRateLimit  = 5
	signupRateWindow = time.Hour
)

// RegisterPublicRoutes mounts POST /api/auth/signup on the PUBLIC mux (outside AuthMiddleware) —
// the whole point of the route is creating the account/session. It is registered only when signup
// is enabled, so a deployment that hasn't opted in simply 404s.
func RegisterPublicRoutes(mux *http.ServeMux, handler *Handler) {
	if handler == nil || !handler.cfg.Enabled {
		return
	}
	limiter := httpx.NewRateLimiter(signupRateLimit, signupRateWindow)
	mux.HandleFunc("POST /api/auth/signup", httpx.Wrap(httpx.RateLimit(limiter, handler.Signup)))
}
