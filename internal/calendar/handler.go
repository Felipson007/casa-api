package calendar

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles GET /api/calendar/events?from=YYYY-MM-DD&to=YYYY-MM-DD.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	from, err := parseRangeDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		return err
	}
	to, err := parseRangeDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		return err
	}
	dtos, err := h.service.ListRange(r.Context(), from, to)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Create handles POST /api/calendar/events.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateEventRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Create(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// Update handles PUT /api/calendar/events/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[UpdateEventRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Update(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Delete handles DELETE /api/calendar/events/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
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
