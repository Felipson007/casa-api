package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeCheck is a CheckPermFunc for unit tests — denies everything except the explicit allow map.
func fakeCheck(allow map[string]bool) CheckPermFunc {
	return func(_ context.Context, u User, module string, mode PermMode) (bool, error) {
		if u.Role == "OWNER" {
			return true, nil
		}
		return allow[module+":"+string(mode)], nil
	}
}

func TestRequirePerm_OwnerBypass(t *testing.T) {
	httpxDefault := DefaultCheckPerm
	DefaultCheckPerm = fakeCheck(nil) // member would be denied, but owner bypasses
	defer func() { DefaultCheckPerm = httpxDefault }()

	owner := User{ID: uuid.New(), Role: "OWNER"}
	fn := RequirePerm(DefaultCheckPerm, "loans", PermWrite, func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/loans", nil)
	req = req.WithContext(WithUser(req.Context(), owner))
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err != nil {
		t.Fatalf("owner should bypass perm check, got error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", rec.Code)
	}
}

func TestRequirePerm_MemberDeniedWhenNoRow(t *testing.T) {
	httpxDefault := DefaultCheckPerm
	DefaultCheckPerm = fakeCheck(nil) // no member grants
	defer func() { DefaultCheckPerm = httpxDefault }()

	member := User{ID: uuid.New(), Role: "MEMBER"}
	fn := RequirePerm(DefaultCheckPerm, "loans", PermWrite, func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/loans", nil)
	req = req.WithContext(WithUser(req.Context(), member))
	rec := httptest.NewRecorder()
	err := fn(rec, req)
	if err == nil {
		t.Fatal("expected Forbidden error for member without grant")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindForbidden {
		t.Fatalf("expected apperr.Forbidden, got %v", err)
	}
}

func TestRequirePerm_MemberAllowedWhenGranted(t *testing.T) {
	httpxDefault := DefaultCheckPerm
	DefaultCheckPerm = fakeCheck(map[string]bool{"loans:write": true})
	defer func() { DefaultCheckPerm = httpxDefault }()

	member := User{ID: uuid.New(), Role: "MEMBER"}
	fn := RequirePerm(DefaultCheckPerm, "loans", PermWrite, func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/loans", nil)
	req = req.WithContext(WithUser(req.Context(), member))
	rec := httptest.NewRecorder()
	if err := fn(rec, req); err != nil {
		t.Fatalf("member with grant should pass, got: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_UnauthorizedWhenNoHeader(t *testing.T) {
	mw := AuthMiddleware(func(_ context.Context, _ string) (User, error) {
		return User{}, nil
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/loans", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without Authorization header, got %d", rec.Code)
	}
}
