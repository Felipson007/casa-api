package auth

import (
	"net/http"
	"time"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// loginRateLimit / loginRateWindow throttle POST /api/auth/login per client IP. It is an
// unauthenticated route whose whole job is checking a password, so it is the natural brute-force
// target; 10/min still absorbs a human fat-fingering their password a few times.
const (
	loginRateLimit  = 10
	loginRateWindow = time.Minute

	// mfaRateLimit throttles the second-factor step independently: it is a 6-digit (or recovery
	// code) guess surface on top of an already-correct password.
	mfaRateLimit  = 10
	mfaRateWindow = time.Minute

	// securityRateLimit throttles authenticated security mutations (password change, MFA setup /
	// disable / recovery-code regeneration) per IP.
	securityRateLimit  = 20
	securityRateWindow = time.Minute
)

// RegisterPublicRoutes mounts login/logout/Google-OAuth on the PUBLIC mux (outside
// AuthMiddleware) — the entire point of these routes is creating a session, so none of them can
// require one already existing. Mirrors internal/passwordreset/routes.go's public-mux rule. The
// Google routes are registered only if real credentials were provided (Handler.google.Enabled()),
// so an unconfigured deployment doesn't expose an endpoint that would just fail on every attempt.
func RegisterPublicRoutes(mux *http.ServeMux, handler *Handler) {
	loginLimiter := httpx.NewRateLimiter(loginRateLimit, loginRateWindow)
	mfaLimiter := httpx.NewRateLimiter(mfaRateLimit, mfaRateWindow)
	mux.HandleFunc("POST /api/auth/login", httpx.Wrap(httpx.RateLimit(loginLimiter, handler.Login)))
	mux.HandleFunc("POST /api/auth/login/mfa", httpx.Wrap(httpx.RateLimit(mfaLimiter, handler.LoginMFA)))
	mux.HandleFunc("POST /api/auth/logout", httpx.Wrap(handler.Logout))
	if handler.google != nil && handler.google.Enabled() {
		mux.HandleFunc("GET /oauth2/authorization/google", handler.BeginGoogleLogin)
		mux.HandleFunc("GET /login/oauth2/code/google", handler.GoogleCallback)
	}
}

// RegisterAuthenticatedRoutes mounts the self-service auth surface — GET /api/auth/me plus the
// profile/password/MFA endpoints. All need AuthMiddleware to have already resolved the user, so
// they go on the regular authenticated mux like every domain module. Security mutations get a
// per-IP rate limit on top.
func RegisterAuthenticatedRoutes(mux *http.ServeMux, handler *Handler) {
	securityLimiter := httpx.NewRateLimiter(securityRateLimit, securityRateWindow)
	mux.HandleFunc("GET /api/auth/me", httpx.Wrap(handler.Me))
	mux.HandleFunc("PUT /api/auth/perfil", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.UpdateProfile)))
	mux.HandleFunc("PUT /api/auth/senha", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.ChangePassword)))
	mux.HandleFunc("POST /api/auth/mfa/setup", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.MFASetup)))
	mux.HandleFunc("POST /api/auth/mfa/activate", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.MFAActivate)))
	mux.HandleFunc("POST /api/auth/mfa/disable", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.MFADisable)))
	mux.HandleFunc("POST /api/auth/mfa/recovery-codes", httpx.Wrap(httpx.RateLimit(securityLimiter, handler.MFARegenerateRecoveryCodes)))
}
