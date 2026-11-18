package dashboard

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
	"github.com/google/uuid"
)

// Handler is the HTTP adapter. It never touches any repository directly — only the composed
// domain services (see Service).
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// KPIs handles GET /api/dashboard/kpis?year=&month=.
func (h *Handler) KPIs(w http.ResponseWriter, r *http.Request) error {
	year, month, err := parseYearMonth(r)
	if err != nil {
		return err
	}
	dto, err := h.service.KPIs(r.Context(), year, month)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// KPIsBatch handles GET /api/dashboard/kpis-batch?year=&month=&months=.
func (h *Handler) KPIsBatch(w http.ResponseWriter, r *http.Request) error {
	year, month, err := parseYearMonth(r)
	if err != nil {
		return err
	}
	monthsHistory, err := strconv.Atoi(r.URL.Query().Get("months"))
	if err != nil || monthsHistory < 1 {
		return apperr.Validation("months is required and must be a positive integer", "months")
	}
	dtos, err := h.service.KPIsBatch(r.Context(), year, month, monthsHistory)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Resumo handles GET /api/dashboard/resumo?months=.
func (h *Handler) Resumo(w http.ResponseWriter, r *http.Request) error {
	monthsHistory := 12
	if raw := r.URL.Query().Get("months"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return apperr.Validation("months must be a positive integer", "months")
		}
		monthsHistory = parsed
	}
	dto, err := h.service.Resumo(r.Context(), monthsHistory)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Calendar handles GET /api/dashboard/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD — every dated event
// in the range: loan instalments, birthdays (people and pets), and document expirations. The range
// is required and explicit so the calendar can render whatever month the user navigates to,
// including months in the future.
func (h *Handler) Calendar(w http.ResponseWriter, r *http.Request) error {
	from, err := parseRangeDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		return err
	}
	to, err := parseRangeDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		return err
	}
	if to.Before(from) {
		return apperr.Validation("to must not be before from", "to")
	}
	events, err := h.service.CalendarEvents(r.Context(), from, to)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, events)
	return nil
}

func parseRangeDate(raw, field string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, apperr.Validation(field+" is required (YYYY-MM-DD)", field)
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, apperr.Validation(field+" must be YYYY-MM-DD", field)
	}
	return parsed, nil
}

// NetWorth handles GET /api/dashboard/net-worth.
func (h *Handler) NetWorth(w http.ResponseWriter, r *http.Request) error {
	dto, err := h.service.NetWorth(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// PersonDetail handles GET /api/people/{id}/detail — the single-call aggregate the frontend's
// pessoa detalhe page consumes (basic info + health profile + records + documents + assets).
// Replaces the six separate calls (three of which 404'd on the old Java paths) with one.
// Routed here because it composes the people + health + documents + assets services.
func (h *Handler) PersonDetail(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	dto, err := h.service.PersonDetail(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

func parseYearMonth(r *http.Request) (year, month int, err error) {
	year, err = strconv.Atoi(r.URL.Query().Get("year"))
	if err != nil {
		return 0, 0, apperr.Validation("year is required and must be an integer", "year")
	}
	month, err = strconv.Atoi(r.URL.Query().Get("month"))
	if err != nil || month < 1 || month > 12 {
		return 0, 0, apperr.Validation("month is required and must be between 1 and 12", "month")
	}
	return year, month, nil
}
