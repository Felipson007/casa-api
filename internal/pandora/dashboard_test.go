package pandora

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

func testHandler(cfg DashboardConfig) *Handler {
	return NewHandler(NewService(NewStaticResolver(Connector{})), cfg)
}

// TestDashboardChat_RequiresToken — audit F2: the public route was fully unauthenticated.
func TestDashboardChat_RequiresToken(t *testing.T) {
	h := testHandler(DashboardConfig{Token: "s3cret"})

	for name, req := range map[string]*http.Request{
		"no header":     httptest.NewRequest(http.MethodPost, "/api/public/dashboard-chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`)),
		"wrong bearer":  withHeader(httptest.NewRequest(http.MethodPost, "/api/public/dashboard-chat", strings.NewReader(`{}`)), "Authorization", "Bearer nope"),
		"wrong x-token": withHeader(httptest.NewRequest(http.MethodPost, "/api/public/dashboard-chat", strings.NewReader(`{}`)), "X-Dashboard-Token", "nope"),
	} {
		err := h.DashboardChat(httptest.NewRecorder(), req)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Kind != apperr.KindUnauthorized {
			t.Fatalf("%s: expected Unauthorized, got %v", name, err)
		}
	}
}

// TestDashboardChat_RateLimited — F2: no throttle meant unbounded LLM spend + homelab load.
func TestDashboardChat_RateLimited(t *testing.T) {
	h := testHandler(DashboardConfig{Token: "s3cret"})
	// Exhaust the window directly (the allowed calls would otherwise fan out to a real gateway).
	for i := 0; i < dashboardRateLimit; i++ {
		if !h.dashLimiter.Allow("1.2.3.4") {
			t.Fatalf("call %d should have been allowed", i)
		}
	}
	req := withHeader(
		httptest.NewRequest(http.MethodPost, "/api/public/dashboard-chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`)),
		"Authorization", "Bearer s3cret",
	)
	req.RemoteAddr = "1.2.3.4:5555"
	err := h.DashboardChat(httptest.NewRecorder(), req)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindRateLimited {
		t.Fatalf("expected RateLimited once the window is exhausted, got %v", err)
	}
}

// TestWithDashboardCORS_NoReflection — F2: the wrapper used to echo any Origin.
func TestWithDashboardCORS_NoReflection(t *testing.T) {
	noop := func(w http.ResponseWriter, _ *http.Request) {}

	// No allowlist configured: never emit ACAO, whatever the caller claims.
	rec := httptest.NewRecorder()
	req := withHeader(httptest.NewRequest(http.MethodOptions, "/api/public/dashboard-chat", nil), "Origin", "https://evil.example")
	withDashboardCORS("")(noop)(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("no allowlist: expected no ACAO header, got %q", got)
	}

	// Allowlist set: echo only the exact match.
	allowed := "https://atlas-server.taile22ede.ts.net"
	rec = httptest.NewRecorder()
	withDashboardCORS(allowed)(noop)(rec, withHeader(httptest.NewRequest(http.MethodOptions, "/x", nil), "Origin", "https://evil.example"))
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("mismatched Origin must not get an ACAO header")
	}
	rec = httptest.NewRecorder()
	withDashboardCORS(allowed)(noop)(rec, withHeader(httptest.NewRequest(http.MethodOptions, "/x", nil), "Origin", allowed))
	if rec.Header().Get("Access-Control-Allow-Origin") != allowed {
		t.Fatalf("the allowlisted Origin should be echoed, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// Comma-separated list: every listed Origin matches, nothing else does, and the response
	// echoes back the specific Origin that matched (not the whole list).
	list := " http://192.168.0.200 , https://atlas-server.taile22ede.ts.net ,http://atlas-server "
	for _, ok := range []string{"http://192.168.0.200", "https://atlas-server.taile22ede.ts.net", "http://atlas-server"} {
		rec = httptest.NewRecorder()
		withDashboardCORS(list)(noop)(rec, withHeader(httptest.NewRequest(http.MethodOptions, "/x", nil), "Origin", ok))
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != ok {
			t.Fatalf("listed Origin %q should be echoed, got %q", ok, got)
		}
	}
	rec = httptest.NewRecorder()
	withDashboardCORS(list)(noop)(rec, withHeader(httptest.NewRequest(http.MethodOptions, "/x", nil), "Origin", "http://192.168.0.201"))
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("an Origin not in the list must not get an ACAO header")
	}
}

// TestRegisterPublicRoutes_NotMountedWithoutToken — F2 fail-closed.
func TestRegisterPublicRoutes_NotMountedWithoutToken(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPublicRoutes(mux, testHandler(DashboardConfig{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/public/dashboard-chat", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with no DASHBOARD_CHAT_TOKEN the route must not exist, got status %d", rec.Code)
	}
}

func withHeader(r *http.Request, k, v string) *http.Request {
	r.Header.Set(k, v)
	return r
}
