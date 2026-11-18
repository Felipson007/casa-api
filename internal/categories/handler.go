package categories

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter — mirrors CategoriaController's 3 endpoints. It never touches the
// repository directly (docs/architecture-plan.md section 1): every method just decodes/validates
// the request, calls Service, and responds.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles GET /api/categories?type=EXPENSE.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var filterType *Type
	if raw := r.URL.Query().Get("type"); raw != "" {
		t := Type(raw)
		filterType = &t
	}

	dtos, err := h.service.List(r.Context(), filterType)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Create handles POST /api/categories.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateCategoryRequest](r)
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

// Delete handles DELETE /api/categories/{id}.
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
