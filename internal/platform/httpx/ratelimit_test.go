package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

func TestRateLimiter_WindowResets(t *testing.T) {
	rl := NewRateLimiter(2, 30*time.Millisecond)
	if !rl.Allow("k") || !rl.Allow("k") || rl.Allow("k") {
		t.Fatalf("limiter should allow exactly 2 in the window")
	}
	if !rl.Allow("other") {
		t.Fatalf("a different key has its own window")
	}
	time.Sleep(40 * time.Millisecond)
	if !rl.Allow("k") {
		t.Fatalf("limiter should allow again after the window elapses")
	}
}

func TestRateLimit_Middleware(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	var calls int
	h := RateLimit(rl, func(http.ResponseWriter, *http.Request) error { calls++; return nil })

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "9.9.9.9:1"

	if err := h(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("first call should pass: %v", err)
	}
	err := h(httptest.NewRecorder(), req)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindRateLimited {
		t.Fatalf("second call should be rate-limited, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("the limited call must not reach the handler (calls=%d)", calls)
	}
}

func TestClientIP_PrefersXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	if got := ClientIP(req); got != "10.0.0.1" {
		t.Fatalf("no XFF: want remote host, got %q", got)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := ClientIP(req); got != "203.0.113.7" {
		t.Fatalf("want left-most XFF entry, got %q", got)
	}
}
