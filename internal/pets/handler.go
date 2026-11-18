package pets

import (
	"net/http"
	"strconv"
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

// List handles GET /api/pets and GET /api/pets?active=true|false.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var activeOnly *bool
	if raw := r.URL.Query().Get("active"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return apperr.Validation("active must be true or false", "active")
		}
		activeOnly = &parsed
	}

	dtos, err := h.service.List(r.Context(), activeOnly)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/pets/{id}.
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

// Create handles POST /api/pets.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreatePetRequest](r)
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

// Update handles PUT /api/pets/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdatePetRequest](r)
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

// Delete handles DELETE /api/pets/{id}.
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

// ListRecords handles GET /api/pets/{petId}/records.
func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) error {
	petID, err := uuid.Parse(r.PathValue("petId"))
	if err != nil {
		return apperr.Validation("petId must be a valid UUID", "petId")
	}
	dtos, err := h.service.ListRecords(r.Context(), petID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// ListUpcomingVaccines handles GET /api/pets/{petId}/records/upcoming-vaccines.
func (h *Handler) ListUpcomingVaccines(w http.ResponseWriter, r *http.Request) error {
	petID, err := uuid.Parse(r.PathValue("petId"))
	if err != nil {
		return apperr.Validation("petId must be a valid UUID", "petId")
	}
	dtos, err := h.service.ListUpcomingVaccines(r.Context(), petID, time.Now())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// CreateRecord handles POST /api/pets/{petId}/records.
func (h *Handler) CreateRecord(w http.ResponseWriter, r *http.Request) error {
	petID, err := uuid.Parse(r.PathValue("petId"))
	if err != nil {
		return apperr.Validation("petId must be a valid UUID", "petId")
	}
	req, err := httpx.DecodeAndValidate[CreateRecordRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.CreateRecord(r.Context(), petID, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// DeleteRecord handles DELETE /api/pets/{petId}/records/{id}.
func (h *Handler) DeleteRecord(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	if err := h.service.DeleteRecord(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
