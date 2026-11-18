package audit

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

// List handles GET /api/audit-logs and GET /api/audit-logs?tableName=...&rowId=....
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var rowID *uuid.UUID
	if raw := r.URL.Query().Get("rowId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return apperr.Validation("rowId must be a valid UUID", "rowId")
		}
		rowID = &parsed
	}

	dtos, err := h.service.List(r.Context(), r.URL.Query().Get("tableName"), rowID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/audit-logs/{id}.
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

// Create handles POST /api/audit-logs.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateAuditLogRequest](r)
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
