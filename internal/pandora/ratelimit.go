package pandora

import "time"

// dashboardRateLimit / dashboardRateWindow bound POST /api/public/dashboard-chat, per client, per
// window. The endpoint is token-gated (a shared secret proves "is the widget", not "which user")
// and every call is an LLM round-trip on a paid gateway key, so an unbounded caller is a
// cost-amplification problem. A dashboard
// widget polls a handful of times a minute at most; 20 leaves generous headroom. The limiter
// itself is internal/platform/httpx.RateLimiter (shared with the auth brute-force limits).
const (
	dashboardRateLimit  = 20
	dashboardRateWindow = time.Minute
)
