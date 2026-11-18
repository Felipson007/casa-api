package httpx

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// RejectCrossSiteMutations is a defense-in-depth CSRF guard. For unsafe methods
// (POST/PUT/PATCH/DELETE) it requires the request's Origin (or, failing that, Referer) to match
// either selfOrigin (APP_FRONTEND_ORIGIN) or the request's own scheme://host. A mismatch is 403.
//
// Requests that carry neither Origin nor Referer are allowed: those are non-browser clients
// (scripts/tests using "Authorization: Bearer"), and a browser always sends Origin on a
// cross-site unsafe request. This complements the session cookie's SameSite=Lax — the API
// validates no CSRF token of its own, so before this the only thing standing between a malicious
// page and a state-changing call was that one cookie attribute.
//
// /api/public/* is exempt: those routes are deliberately cross-origin and carry their own auth
// (the dashboard-chat shared token + an explicit CORS allowlist).
func RejectCrossSiteMutations(selfOrigin string) func(http.Handler) http.Handler {
	self := strings.TrimRight(selfOrigin, "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafeMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/api/public/") {
				next.ServeHTTP(w, r)
				return
			}
			claimed := r.Header.Get("Origin")
			if claimed == "" {
				if ref := r.Header.Get("Referer"); ref != "" {
					if u, err := url.Parse(ref); err == nil {
						claimed = u.Scheme + "://" + u.Host
					}
				}
			}
			if claimed == "" {
				next.ServeHTTP(w, r) // no browser context — bearer-token script/test
				return
			}
			if claimed == self || claimed == requestOrigin(r) {
				next.ServeHTTP(w, r)
				return
			}
			WriteProblem(w, apperr.Forbidden("cross-site request rejected"))
		})
	}
}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// requestOrigin reconstructs the browser's notion of this request's own origin: scheme://host,
// honouring a TLS-terminating reverse proxy's X-Forwarded-Proto (same rule the session cookie's
// Secure flag uses).
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
