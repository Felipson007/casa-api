package httpx

import (
	"net/http"
	"time"
)

// SetSessionCookie writes the session cookie used by both the login/OAuth handlers (internal/auth)
// and the self-service signup handler (internal/tenancy). Secure is marked whenever the request
// itself arrived over TLS, directly or via a reverse proxy that terminates it (X-Forwarded-Proto) —
// never hardcoded, since local testing runs over plain HTTP (a hardcoded Secure would make the
// browser silently drop the cookie) while a TLS-terminated deployment gets it automatically.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: expiresAt, Secure: isRequestSecure(r),
	})
}

// ClearSessionCookie expires the session cookie client-side (logout).
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1})
}

func isRequestSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
