package digitalaccounts

import (
	"net/http"

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

// List handles GET /api/digital-accounts, optionally filtered by ?personId=, ?category=,
// ?status=, and/or ?email= (exact match on emailUsed — the "which accounts still use my old
// Gmail" query, GAP 1).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	personID, err := parseOptionalUUID(r, "personId")
	if err != nil {
		return err
	}
	var category *Category
	if raw := r.URL.Query().Get("category"); raw != "" {
		c := Category(raw)
		category = &c
	}
	var status *Status
	if raw := r.URL.Query().Get("status"); raw != "" {
		s := Status(raw)
		status = &s
	}
	var email *string
	if raw := r.URL.Query().Get("email"); raw != "" {
		email = &raw
	}

	dtos, err := h.service.List(r.Context(), personID, category, status, email)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/digital-accounts/{id}.
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

// Create handles POST /api/digital-accounts.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateAccountRequest](r)
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

// Import handles POST /api/digital-accounts/import — bulk create with per-item duplicate skipping.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[ImportAccountsRequest](r)
	if err != nil {
		return err
	}

	result, err := h.service.Import(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

// Update handles PUT /api/digital-accounts/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdateAccountRequest](r)
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

// MarkReviewed handles POST /api/digital-accounts/{id}/mark-reviewed.
func (h *Handler) MarkReviewed(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dto, err := h.service.MarkReviewed(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Delete handles DELETE /api/digital-accounts/{id}.
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
