package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// alwaysAllow / alwaysDeny / errCheck are CheckPermFunc fakes for the HTTP smoke test.
func alwaysAllow(context.Context, User, string, PermMode) (bool, error) { return true, nil }
func alwaysDeny(context.Context, User, string, PermMode) (bool, error)  { return false, nil }
func errCheck(context.Context, User, string, PermMode) (bool, error) {
	return false, apperr.Internal(nil)
}

// okHandler is a HandlerFunc (returns error) that writes 200.
func okHandler(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
	return nil
}

// ownerAwareDeny denies everyone except OWNER — mirrors how auth.New's CheckPermFunc behaves
// (the OWNER bypass lives in the injected CheckPermFunc, not in RequirePerm itself).
func ownerAwareDeny(ctx context.Context, u User, module string, mode PermMode) (bool, error) {
	if u.Role == "OWNER" {
		return true, nil
	}
	return false, nil
}

// TestRequirePerm_OwnerBypassesCheck: OWNER passes even when a naive deny would reject (the
// CheckPermFunc is responsible for the OWNER bypass).
func TestRequirePerm_OwnerBypassesCheck(t *testing.T) {
	fn := RequirePerm(ownerAwareDeny, "expenses", PermWrite, okHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/expenses", nil).WithContext(
		WithUser(context.Background(), User{ID: uuid.New(), Role: "OWNER"}))
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err != nil {
		t.Fatalf("owner should bypass deny-check, got err %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("owner should get 200, got %d", rec.Code)
	}
}

// TestRequirePerm_MemberAllowed: MEMBER with an allow check passes.
func TestRequirePerm_MemberAllowed(t *testing.T) {
	fn := RequirePerm(alwaysAllow, "expenses", PermWrite, okHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/expenses", nil).WithContext(
		WithUser(context.Background(), User{ID: uuid.New(), Role: "MEMBER"}))
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err != nil {
		t.Fatalf("member with allow-check should pass, got err %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member should get 200, got %d", rec.Code)
	}
}

// TestRequirePerm_MemberDenied: MEMBER without permission gets apperr.Forbidden.
func TestRequirePerm_MemberDenied(t *testing.T) {
	fn := RequirePerm(alwaysDeny, "expenses", PermWrite, okHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/expenses", nil).WithContext(
		WithUser(context.Background(), User{ID: uuid.New(), Role: "MEMBER"}))
	rec := httptest.NewRecorder()
	err := fn(rec, req)
	if err == nil {
		t.Fatal("expected an error (maps to 403)")
	}
	if e, ok := err.(*apperr.Error); !ok || e.Kind != apperr.KindForbidden {
		t.Fatalf("expected apperr.Forbidden, got %v", err)
	}
}

// TestRequirePerm_MissingUser: no User in context -> Unauthorized.
func TestRequirePerm_MissingUser(t *testing.T) {
	fn := RequirePerm(alwaysAllow, "expenses", PermWrite, okHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/expenses", nil)
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err == nil {
		t.Fatal("expected an error when no User in context")
	}
}

// TestRequirePerm_CheckError: a failing CheckPermFunc surfaces as an internal error.
func TestRequirePerm_CheckError(t *testing.T) {
	fn := RequirePerm(errCheck, "expenses", PermWrite, okHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/expenses", nil).WithContext(
		WithUser(context.Background(), User{ID: uuid.New(), Role: "MEMBER"}))
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err == nil {
		t.Fatal("expected an error when CheckPerm fails")
	}
}

// TestAuthMiddleware_InjectsUser: a load func returning a User lands it in the context. The token
// resolved is an opaque session token, not a parseable UUID (real auth resolves it via a database
// lookup, not by parsing it) — the fake load func here ignores its value entirely.
func TestAuthMiddleware_InjectsUser(t *testing.T) {
	var seen string
	h := AuthMiddleware(func(context.Context, string) (User, error) {
		return User{ID: uuid.New(), Role: "OWNER"}, nil
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFromContext(r.Context()); ok {
			seen = u.Role
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer opaque-session-token")
	h.ServeHTTP(rec, req)
	if seen != "OWNER" {
		t.Fatalf("expected OWNER injected into context, got %q", seen)
	}
}

// TestAuthMiddleware_UnknownUserNil: a load func returning a nil User -> 401.
func TestAuthMiddleware_UnknownUserNil(t *testing.T) {
	h := AuthMiddleware(func(context.Context, string) (User, error) {
		return User{}, nil // ID == uuid.Nil
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer opaque-session-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown user, got %d", rec.Code)
	}
}

// TestAuthMiddleware_MissingToken: no cookie and no Authorization header -> 401.
func TestAuthMiddleware_MissingToken(t *testing.T) {
	h := AuthMiddleware(func(context.Context, string) (User, error) {
		t.Fatal("load should not be called when no token is present")
		return User{}, nil
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", rec.Code)
	}
}

// TestAuthMiddleware_ReadsSessionCookie: the session cookie is read when no Authorization header
// is present — the browser's normal path (no header is ever set client-side; the cookie rides
// along automatically).
func TestAuthMiddleware_ReadsSessionCookie(t *testing.T) {
	var gotToken string
	h := AuthMiddleware(func(_ context.Context, token string) (User, error) {
		gotToken = token
		return User{ID: uuid.New(), Role: "OWNER"}, nil
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "cookie-session-token"})
	h.ServeHTTP(rec, req)
	if gotToken != "cookie-session-token" {
		t.Fatalf("expected token from cookie, got %q", gotToken)
	}
}
