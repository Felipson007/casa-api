package aiconnectors

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler serves the per-tenant AI connector CRUD. Registered on the authenticated mux, so the
// context already carries the tenant (schema + id) and every operation is scoped to it.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	cs, err := h.service.List(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTOs(cs))
	return nil
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[UpsertRequest](r)
	if err != nil {
		return err
	}
	c, err := h.service.Create(r.Context(), req.toInput())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(*c))
	return nil
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid connector id")
	}
	req, err := httpx.DecodeAndValidate[UpsertRequest](r)
	if err != nil {
		return err
	}
	c, err := h.service.Update(r.Context(), id, req.toInput())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(*c))
	return nil
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid connector id")
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
