package pandora

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// DashboardConfig carries the settings that harden the one unauthenticated route
// (POST /api/public/dashboard-chat). Token is a shared secret the dashboard widget must present
// "Authorization: Bearer <token>"; an empty Token means the route is not registered at all (see
// RegisterPublicRoutes). AllowedOrigins is DASHBOARD_CHAT_ALLOWED_ORIGIN — a comma-separated
// list of exact Origins the route answers CORS for (the widget reaches the same page from the LAN
// IP, the Tailscale name, etc., so each is a distinct Origin). Empty => no CORS headers at all.
// The previous behaviour reflected any Origin, which let any site a tailnet user visited read the
// response.
type DashboardConfig struct {
	Token          string
	AllowedOrigins string
}

// Handler proxies chat traffic to the OmniRoute gateway, injecting the configured API key. The
// browser only ever talks to atlas-go (same origin), so no CORS is involved on the authenticated
// hop.
type Handler struct {
	svc         *Service
	httpClient  *http.Client
	dashboard   DashboardConfig
	dashLimiter *httpx.RateLimiter
}

func NewHandler(svc *Service, dashboard DashboardConfig) *Handler {
	return &Handler{
		svc:         svc,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
		dashboard:   dashboard,
		dashLimiter: httpx.NewRateLimiter(dashboardRateLimit, dashboardRateWindow),
	}
}

// dashboardEnabled reports whether the public dashboard-chat route should be mounted at all.
func (h *Handler) dashboardEnabled() bool { return h.dashboard.Token != "" }

// authorizeDashboard checks the shared token on POST /api/public/dashboard-chat. Constant-time
// compare; accepts "Authorization: Bearer <token>" or a bare "X-Dashboard-Token: <token>".
func (h *Handler) authorizeDashboard(r *http.Request) error {
	presented := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if presented == "" {
		presented = strings.TrimSpace(r.Header.Get("X-Dashboard-Token"))
	}
	if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(h.dashboard.Token)) != 1 {
		return apperr.Unauthorized("missing or invalid dashboard token")
	}
	return nil
}

// proxyChatMaxBodyBytes / proxyChatMaxTokens bound what an authenticated caller can ask the paid
// gateway to do on POST /api/pandora/chat. The model is pinned server-side (OMNIROUTE_MODEL) — the
// caller no longer picks an arbitrary/expensive one — and max_tokens is clamped.
const (
	proxyChatMaxBodyBytes = 256 * 1024
	proxyChatMaxTokens    = 4096
)

// ProxyChat forwards POST /api/pandora/chat to the OmniRoute gateway's /v1/chat/completions using
// the configured API key. The caller supplies the message history; "model" is forced to the
// server-configured one and "max_tokens" is clamped, so a read-only user can't turn this into
// unbounded third-party spend. Only an allowlist of response headers is passed back (previously
// every upstream header was echoed verbatim).
func (h *Handler) ProxyChat(w http.ResponseWriter, r *http.Request) error {
	conn, err := h.svc.resolve(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoProvider) {
			return apperr.Validation("configure um provedor de IA em Configurações para usar este recurso")
		}
		return apperr.Internal(err)
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, proxyChatMaxBodyBytes))
	if err != nil {
		return apperr.Validation("request body too large or unreadable")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return apperr.Validation("invalid JSON body")
	}
	payload["model"] = conn.ChatModel
	payload["stream"] = false
	if mt, ok := payload["max_tokens"].(float64); !ok || mt <= 0 || mt > proxyChatMaxTokens {
		payload["max_tokens"] = proxyChatMaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return apperr.Internal(err)
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, conn.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return apperr.Internal(err)
	}
	upReq.Header.Set("Content-Type", "application/json")
	applyAuth(upReq, conn)

	resp, err := h.httpClient.Do(upReq)
	if err != nil {
		return apperr.Internal(err)
	}
	defer resp.Body.Close()

	for _, k := range []string{"Content-Type", "Cache-Control", "Retry-After"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
	return nil
}

// dashboardChatRequest is the external dashboard widget's request body — just a message history, no
// model field: the model is pinned server-side (see DashboardChat) so a public caller can't pick
// an arbitrary/expensive one.
type dashboardChatRequest struct {
	Messages []ChatMessage `json:"messages"`
}

const dashboardChatMaxMessages = 40
const dashboardChatMaxBodyBytes = 64 * 1024

// DashboardChat handles POST /api/public/dashboard-chat — served outside AuthMiddleware (the
// dashboard is a different origin with no atlas-go login of its own) but gated by the
// DASHBOARD_CHAT_TOKEN shared secret and rate-limited per IP. Rather than give the widget the raw
// OMNIROUTE_API_KEY (which a browser can't keep secret), it sends its message history here and
// this handler is the only thing that ever holds the key.
func (h *Handler) DashboardChat(w http.ResponseWriter, r *http.Request) error {
	if err := h.authorizeDashboard(r); err != nil {
		return err
	}
	if !h.dashLimiter.Allow(httpx.ClientIP(r)) {
		return apperr.RateLimited("dashboard chat rate limit exceeded — slow down")
	}
	r.Body = http.MaxBytesReader(w, r.Body, dashboardChatMaxBodyBytes)

	var req dashboardChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.Validation("invalid JSON body", "messages")
	}
	if len(req.Messages) == 0 {
		return apperr.Validation("messages is required", "messages")
	}
	if len(req.Messages) > dashboardChatMaxMessages {
		return apperr.Validation("too many messages", "messages")
	}

	messages := make([]ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ChatCompletionMessage{Role: m.Role, Content: m.Content})
	}

	reply, err := h.svc.ChatCompletion(r.Context(), messages, nil)
	if err != nil {
		return apperr.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"reply": reply.Content})
	return nil
}
