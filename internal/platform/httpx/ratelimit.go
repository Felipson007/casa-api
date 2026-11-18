package httpx

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// RateLimiter is a fixed-window request counter keyed by an arbitrary string (usually a client
// IP). In-memory and process-local — all a single-instance homelab deployment needs; stale
// windows are swept lazily. Safe for concurrent use.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*rlWindow
}

type rlWindow struct {
	count int
	start time.Time
}

// NewRateLimiter builds a limiter allowing limit requests per key per window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: make(map[string]*rlWindow)}
}

// Allow records one request from key and reports whether it is within the limit for the current
// window.
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.hits) > 4096 {
		for k, w := range rl.hits {
			if now.Sub(w.start) > rl.window {
				delete(rl.hits, k)
			}
		}
	}

	w, ok := rl.hits[key]
	if !ok || now.Sub(w.start) > rl.window {
		rl.hits[key] = &rlWindow{count: 1, start: now}
		return true
	}
	w.count++
	return w.count <= rl.limit
}

// RateLimit wraps a HandlerFunc, rejecting a caller (keyed by ClientIP) with apperr.RateLimited
// once it exceeds rl's window. Used on the unauthenticated brute-forceable routes (login,
// forgot-password) and the token-gated dashboard-chat proxy.
func RateLimit(rl *RateLimiter, fn HandlerFunc) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if !rl.Allow(ClientIP(r)) {
			return apperr.RateLimited("too many requests — slow down and try again shortly")
		}
		return fn(w, r)
	}
}

// ClientIP is a best-effort client identifier for rate limiting: the left-most X-Forwarded-For
// entry when the request came through the homelab reverse proxy (Traefik), else the connection's
// remote host. Spoofable, like every XFF-based key — good enough to throttle abuse, never used
// for authorization.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
