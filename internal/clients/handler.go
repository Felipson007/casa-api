package clients

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter — mirrors categories.Handler's shape. It never touches the
// repository directly: every method just decodes/validates the request, calls Service, responds.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles GET /api/clients and GET /api/clients?active=true|false.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var activeOnly *bool
	if raw := r.URL.Query().Get("active"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return apperr.Validation("active must be true or false", "active")
		}
		activeOnly = &parsed
	}

	dtos, err := h.service.List(r.Context(), activeOnly, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/clients/{id}.
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

// Create handles POST /api/clients.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateClientRequest](r)
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

// Update handles PUT /api/clients/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdateClientRequest](r)
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

// Delete handles DELETE /api/clients/{id}.
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
