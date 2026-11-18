package httpx

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// User is the authenticated principal, carried in the request context after AuthMiddleware runs.
// OWNER bypasses every RequirePerm check (single-tenant, one owner has full access). MEMBER is
// checked against member_permissions rows (empty = no access to that module/action).
type User struct {
	ID   uuid.UUID
	Role string // "OWNER" | "MEMBER"
	// TenantID is the tenant the user belongs to (app_users.tenant_id).
	TenantID uuid.UUID
	// TenantSchema is the Postgres schema this user's domain queries run in (tenants.schema_name),
	// resolved with the session. Empty means "no tenant binding" — domain queries fall back to the
	// connection default (`public`), which is the owner's schema until the cutover.
	TenantSchema string
}

type ctxKeyUser struct{}

// WithUser stores the authenticated user in the context (used by tests and AuthMiddleware).
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKeyUser{}, u)
}

// UserFromContext returns the user previously stored via WithUser, or false if absent.
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKeyUser{}).(User)
	return u, ok
}

// SessionCookieName is the HttpOnly cookie the browser carries a session token in. Set directly by
// the login/OAuth-callback handlers (internal/auth) — same-origin in both dev (proxied through the
// frontend's rewrites) and the production export build (Go serves the SPA itself), so no CORS/
// cross-site cookie configuration is needed.
const SessionCookieName = "atlas_session"

// LoadUserFunc resolves an opaque session token (from the session cookie, or an Authorization
// header for scripts/tests) to the authenticated user. Returning (User{}, nil) with a zero ID, or a
// non-nil error, fails authentication. Injected by the composition root (main.go) so this package
// stays free of any postgres import — same idiom as the domain Repository interfaces.
type LoadUserFunc func(ctx context.Context, token string) (User, error)

// AuthMiddleware resolves the session token from the atlas_session cookie (browser traffic) or the
// Authorization header (scripts/tests/API clients) and loads the User into the context. A
// missing/invalid token is a 401. A token that resolves to no user (expired, revoked, unknown) is a
// 401 too — LoadUserFunc never distinguishes why for the caller, same as before real auth existed.
//
// Design note (D-003): this is the single shared authz boundary. Every module's routes are wrapped
// by this once at the border; per-module permission checks use RequirePerm, never re-implementing
// the rule. The Claude-owned modules call RequirePerm instead of inventing their own check.
func AuthMiddleware(load LoadUserFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := sessionTokenFromRequest(r)
			if token == "" {
				WriteProblem(w, apperr.Unauthorized("no session"))
				return
			}
			u, err := load(r.Context(), token)
			if err != nil {
				logging.FromContext(r.Context()).Error("session lookup failed", "error", err)
				WriteProblem(w, apperr.Unauthorized("invalid or expired session"))
				return
			}
			if u.ID == uuid.Nil {
				WriteProblem(w, apperr.Unauthorized("invalid or expired session"))
				return
			}
			ctx := WithUser(r.Context(), u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sessionTokenFromRequest reads the session token from the cookie first (the browser's normal
// path), falling back to the Authorization header ("Bearer <token>" or a bare token) for scripts
// and tests that can't rely on a cookie jar.
func sessionTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	raw := r.Header.Get("Authorization")
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}

// PermMode is the access being requested.
type PermMode string

const (
	// PermRead covers GET-style handlers.
	PermRead PermMode = "read"
	// PermWrite covers POST/PUT/DELETE-style handlers.
	PermWrite PermMode = "write"
)

// CheckPermFunc returns whether a user may perform mode on module. OWNER always may. MEMBER is
// checked against member_permissions (missing row = denied). Injected by the composition root.
type CheckPermFunc func(ctx context.Context, u User, module string, mode PermMode) (bool, error)

// RequirePerm wraps a HandlerFunc, enforcing that the authenticated user may perform mode on module.
// OWNER bypasses; MEMBER must have a member_permissions row with the matching can_read/can_write.
// Used at the router level per module (e.g. loans.RegisterRoutes wraps write handlers in
// RequirePerm("loans", PermWrite)).
func RequirePerm(check CheckPermFunc, module string, mode PermMode, fn HandlerFunc) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, ok := UserFromContext(r.Context())
		if !ok {
			return apperr.Unauthorized("no authenticated user in context")
		}
		// OWNER bypasses every check (single-tenant: one owner has full access).
		if u.Role == "OWNER" {
			return fn(w, r)
		}
		allowed, err := check(r.Context(), u, module, mode)
		if err != nil {
			return apperr.Internal(err)
		}
		if !allowed {
			return apperr.Forbidden("user lacks " + string(mode) + " permission on module " + module)
		}
		return fn(w, r)
	}
}

// ErrForbidden is returned when a permission is denied at the store layer (kept for callers that
// build their own checks). RequirePerm maps it to apperr.Forbidden itself.
var ErrForbidden = errors.New("forbidden")

// DefaultCheckPerm is the process-wide CheckPermFunc, set once by the composition root (main.go)
// after auth.New(pool) is constructed. Modules call RequirePerm(httpx.DefaultCheckPerm, ...) so they
// don't need the function threaded through every RegisterRoutes signature. If unset, RequirePerm
// treats every non-owner as denied.
var DefaultCheckPerm CheckPermFunc
