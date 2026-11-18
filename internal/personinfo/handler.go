package personinfo

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

// List handles GET /api/person-information?personId=<uuid>.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	personID, err := uuid.Parse(r.URL.Query().Get("personId"))
	if err != nil {
		return apperr.Validation("personId is required and must be a valid UUID", "personId")
	}

	dtos, err := h.service.List(r.Context(), personID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/person-information/{id}.
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

// Create handles POST /api/person-information.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateInformationRequest](r)
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

// CreateBatch handles POST /api/person-information/batch.
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateInformationBatchRequest](r)
	if err != nil {
		return err
	}

	dtos, err := h.service.CreateBatch(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dtos)
	return nil
}

// Update handles PUT /api/person-information/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdateInformationRequest](r)
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

// Delete handles DELETE /api/person-information/{id}.
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

// ListHistory handles GET /api/person-information/{id}/history.
func (h *Handler) ListHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dtos, err := h.service.ListHistory(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Equalize handles POST /api/person-information/equalize.
func (h *Handler) Equalize(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[EqualizeInformationRequest](r)
	if err != nil {
		return err
	}

	if err := h.service.Equalize(r.Context(), req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
