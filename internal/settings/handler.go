package settings

import (
	"errors"
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP surface for settings.
type Handler struct {
	service *Service
}

// NewHandler builds a settings handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	s, err := h.service.Get(r.Context())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("settings not found")
		}
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(s))
	return nil
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[UpdateSettingsRequest](r)
	if err != nil {
		return err
	}
	s, err := h.service.Update(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("settings not found")
		}
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(s))
	return nil
}
