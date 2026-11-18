package auditevents

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

const (
	maxClientMessageLen = 500
	maxClientDetailLen  = 8000
)

// Handler is the HTTP adapter; it never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles GET /api/audit-events?tipo=&severidade=&busca=&de=&ate=&pagina=&tamanho=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	filter := Filter{
		Type:     strings.TrimSpace(q.Get("tipo")),
		Severity: strings.TrimSpace(q.Get("severidade")),
		Search:   q.Get("busca"),
	}
	from, err := parseTimeParam(q.Get("de"), "de")
	if err != nil {
		return err
	}
	to, err := parseTimeParam(q.Get("ate"), "ate")
	if err != nil {
		return err
	}
	filter.From, filter.To = from, to

	page := parseIntDefault(q.Get("pagina"), 0)
	size := parseIntDefault(q.Get("tamanho"), DefaultPageSize)
	if page < 0 {
		page = 0
	}
	if size <= 0 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	filter.Limit = size
	filter.Offset = page * size

	result, err := h.service.List(r.Context(), filter)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, ListResponse{
		Items:        toDTOs(result.Items),
		Total:        result.Total,
		Pagina:       result.Page,
		Tamanho:      result.Size,
		TotalPaginas: result.TotalPages,
	})
	return nil
}

// Stats handles GET /api/audit-events/stats?dias=7.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) error {
	days := parseIntDefault(r.URL.Query().Get("dias"), DefaultStatsDays)
	stats, err := h.service.Stats(r.Context(), days)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toStatsDTO(stats))
	return nil
}

// Get handles GET /api/audit-events/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	ev, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(ev))
	return nil
}

// Clear handles DELETE /api/audit-events — the owner's manual "limpar auditoria" action.
func (h *Handler) Clear(w http.ResponseWriter, r *http.Request) error {
	n, err := h.service.Clear(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"removidos": n})
	return nil
}

// ReportClientError handles POST /api/audit-events/client — an uncaught frontend exception. Any
// authenticated user may report; the route is rate-limited at registration.
func (h *Handler) ReportClientError(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[ClientReportRequest](r)
	if err != nil {
		return err
	}

	ev := eventlog.Event{
		Type:      eventlog.TypeClientError,
		Severity:  eventlog.SeverityError,
		IP:        httpx.ClientIP(r),
		Method:    r.Method,
		Path:      strings.TrimSpace(req.Caminho),
		Message:   truncate(strings.TrimSpace(req.Mensagem), maxClientMessageLen),
		Detail:    truncate(strings.TrimSpace(req.Stack), maxClientDetailLen),
		Metadata:  map[string]any{"origem": "frontend"},
		CreatedAt: time.Now(),
	}
	if u, ok := httpx.UserFromContext(r.Context()); ok {
		id := u.ID
		ev.UserID = &id
	}
	if err := h.service.Record(r.Context(), ev); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func parseTimeParam(raw, field string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, apperr.Validation(field+" must be an RFC3339 timestamp", field)
	}
	return &t, nil
}

func parseIntDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
