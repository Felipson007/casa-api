package expenses

import (
	"net/http"
	"strconv"
	"strings"
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

func parseOptionalUUID(r *http.Request, param string) (*uuid.UUID, error) {
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperr.Validation(param+" must be a valid UUID", param)
	}
	return &parsed, nil
}

// List handles GET /api/expenses, optionally filtered by ?categoryId=, ?personId= and/or ?q=
// (case-insensitive description search).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	categoryID, err := parseOptionalUUID(r, "categoryId")
	if err != nil {
		return err
	}
	personID, err := parseOptionalUUID(r, "personId")
	if err != nil {
		return err
	}

	dtos, err := h.service.List(r.Context(), categoryID, personID, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/expenses/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dto, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Create handles POST /api/expenses.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateExpenseRequest](r)
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

// Update handles PUT /api/expenses/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdateExpenseRequest](r)
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

// Delete handles DELETE /api/expenses/{id}.
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

// MonthlySummary handles GET /api/expenses/monthly-summary?year=&month=.
func (h *Handler) MonthlySummary(w http.ResponseWriter, r *http.Request) error {
	year, month, err := parseYearMonth(r)
	if err != nil {
		return err
	}
	dto, err := h.service.MonthlySummary(r.Context(), year, month)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// AnnualSummary handles GET /api/expenses/annual-summary?year=.
func (h *Handler) AnnualSummary(w http.ResponseWriter, r *http.Request) error {
	year, err := strconv.Atoi(r.URL.Query().Get("year"))
	if err != nil {
		return apperr.Validation("year is required and must be an integer", "year")
	}
	dto, err := h.service.AnnualSummary(r.Context(), year)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Subscriptions handles GET /api/expenses/subscriptions.
func (h *Handler) Subscriptions(w http.ResponseWriter, r *http.Request) error {
	dtos, err := h.service.Subscriptions(r.Context(), time.Now())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Autocategorize handles POST /api/expenses/autocategorize: fills every uncategorised expense from
// the household's own category memory and returns how many were filled.
func (h *Handler) Autocategorize(w http.ResponseWriter, r *http.Request) error {
	updated, err := h.service.Autocategorize(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"updated": updated})
	return nil
}

// Uncategorized handles GET /api/expenses/uncategorized: every expense still without a category,
// for the manual-review modal.
func (h *Handler) Uncategorized(w http.ResponseWriter, r *http.Request) error {
	dtos, err := h.service.Uncategorized(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
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
