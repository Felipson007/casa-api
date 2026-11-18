package health

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

func parseUUIDPathValue(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.UUID{}, apperr.Validation(name+" must be a valid UUID", name)
	}
	return id, nil
}

// ListMarkers handles GET /api/health/markers.
func (h *Handler) ListMarkers(w http.ResponseWriter, r *http.Request) error {
	dtos, err := h.service.ListMarkers(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// GetMarker handles GET /api/health/markers/{id}.
func (h *Handler) GetMarker(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	dto, err := h.service.GetMarker(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// CreateMarker handles POST /api/health/markers.
func (h *Handler) CreateMarker(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateMarkerCatalogRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.CreateMarker(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// UpdateMarker handles PUT /api/health/markers/{id}.
func (h *Handler) UpdateMarker(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[UpdateMarkerCatalogRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.UpdateMarker(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// DeleteMarker handles DELETE /api/health/markers/{id}.
func (h *Handler) DeleteMarker(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	if err := h.service.DeleteMarker(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ListRecords handles GET /api/health/records, optionally filtered by ?personId= and/or
// ?markerCatalogId=.
func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) error {
	personID, err := parseOptionalUUID(r, "personId")
	if err != nil {
		return err
	}
	markerCatalogID, err := parseOptionalUUID(r, "markerCatalogId")
	if err != nil {
		return err
	}
	dtos, err := h.service.ListRecords(r.Context(), personID, markerCatalogID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// GetRecord handles GET /api/health/records/{id}.
func (h *Handler) GetRecord(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	dto, err := h.service.GetRecord(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// CreateRecord handles POST /api/health/records.
func (h *Handler) CreateRecord(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateRecordRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.CreateRecord(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// UpdateRecord handles PUT /api/health/records/{id}.
func (h *Handler) UpdateRecord(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[UpdateRecordRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.UpdateRecord(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// DeleteRecord handles DELETE /api/health/records/{id}.
func (h *Handler) DeleteRecord(w http.ResponseWriter, r *http.Request) error {
	id, err := parseUUIDPathValue(r, "id")
	if err != nil {
		return err
	}
	if err := h.service.DeleteRecord(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
