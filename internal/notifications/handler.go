package notifications

import (
	"net/http"
	"strconv"

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

// List handles GET /api/notifications and GET /api/notifications?read=true|false.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var readOnly *bool
	if raw := r.URL.Query().Get("read"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return apperr.Validation("read must be true or false", "read")
		}
		readOnly = &parsed
	}

	dtos, err := h.service.List(r.Context(), readOnly)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/notifications/{id}.
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

// Create handles POST /api/notifications.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateNotificationRequest](r)
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

// MarkRead handles PUT /api/notifications/{id}/read.
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	if err := h.service.MarkRead(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// MarkAllRead handles POST /api/notifications/mark-all-read.
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.MarkAllRead(r.Context()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Delete handles DELETE /api/notifications/{id}.
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

// DeleteAll handles DELETE /api/notifications.
func (h *Handler) DeleteAll(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.DeleteAll(r.Context()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// GetConfiguration handles GET /api/notifications/configuration.
func (h *Handler) GetConfiguration(w http.ResponseWriter, r *http.Request) error {
	dto, err := h.service.GetConfiguration(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// UpdateConfiguration handles PUT /api/notifications/configuration.
func (h *Handler) UpdateConfiguration(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[UpdateConfigurationRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.UpdateConfiguration(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}
