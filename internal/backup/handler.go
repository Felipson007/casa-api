package backup

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP surface for backup configuration.
type Handler struct {
	service *Service
}

// NewHandler builds a backup config handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	cfgs, err := h.service.List(r.Context())
	if err != nil {
		return err
	}
	dtos := make([]BackupConfigDTO, 0, len(cfgs))
	for i := range cfgs {
		dtos = append(dtos, toDTO(&cfgs[i]))
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateBackupConfigRequest](r)
	if err != nil {
		return err
	}
	c, err := h.service.Create(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(c))
	return nil
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	c, err := h.service.Get(r.Context(), id.String())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(c))
	return nil
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[UpdateBackupConfigRequest](r)
	if err != nil {
		return err
	}
	c, err := h.service.Update(r.Context(), id.String(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(c))
	return nil
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	if err := h.service.Delete(r.Context(), id.String()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
