package passwordreset

import (
	"net/http"
	"time"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// forgotPasswordRateLimit / forgotPasswordRateWindow throttle POST /api/auth/forgot-password per
// client IP. Legitimate use is rare (a few times ever, per account), so a tight limit is fine and
// it also blunts the residual account-enumeration timing signal (audit F4) and reset-email spam.
const (
	forgotPasswordRateLimit  = 5
	forgotPasswordRateWindow = 10 * time.Minute
)

// RegisterRoutes mounts this module's endpoints. Composition (postgres.NewRepository -> NewService
// -> NewHandler) happens in cmd/api/main.go (Hermes-owned per D-005).
//
// IMPORTANT — unlike every other module, do NOT wrap these in httpx.RequirePerm, and do NOT
// register them on the same authenticated mux AuthMiddleware wraps: a user requesting or
// completing a password reset is, by definition, not logged in yet (same reasoning as the public
// /health route). Mount RegisterRoutes on the PUBLIC mux instead — the one AuthMiddleware doesn't
// wrap — same as GET /health. Registering these behind AuthMiddleware would make the "forgot
// password" flow impossible to use (you'd need to already be logged in to reset your password).
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	limiter := httpx.NewRateLimiter(forgotPasswordRateLimit, forgotPasswordRateWindow)
	mux.HandleFunc("POST /api/auth/forgot-password", httpx.Wrap(httpx.RateLimit(limiter, handler.RequestReset)))
	mux.HandleFunc("POST /api/auth/reset-password", httpx.Wrap(handler.ResetPassword))
}
