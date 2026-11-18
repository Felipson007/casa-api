package people

import (
	"net/http"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
	"github.com/google/uuid"
)

// Handler exposes the people + relationships endpoints. It decodes/validates, calls the service,
// and serializes — never touches net/http types beyond the signature, and never imports the repo.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ListPeople handles GET /api/people, optionally filtered by ?q= (name/nickname/email).
func (h *Handler) ListPeople(w http.ResponseWriter, r *http.Request) error {
	people, err := h.svc.ListPeople(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, people)
	return nil
}

// CreatePerson godoc.
func (h *Handler) CreatePerson(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreatePersonRequest](r)
	if err != nil {
		return err
	}
	p, err := h.svc.CreatePerson(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
	return nil
}

// GetPerson godoc.
func (h *Handler) GetPerson(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	p, err := h.svc.GetPerson(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, p)
	return nil
}

// UpdatePerson godoc.
func (h *Handler) UpdatePerson(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	req, err := httpx.DecodeAndValidate[UpdatePersonRequest](r)
	if err != nil {
		return err
	}
	p, err := h.svc.UpdatePerson(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, p)
	return nil
}

// DeletePerson godoc.
func (h *Handler) DeletePerson(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	if err := h.svc.DeletePerson(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ListRelationships godoc.
func (h *Handler) ListRelationships(w http.ResponseWriter, r *http.Request) error {
	rels, err := h.svc.ListRelationships(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rels)
	return nil
}

// CreateRelationship godoc.
func (h *Handler) CreateRelationship(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateRelationshipRequest](r)
	if err != nil {
		return err
	}
	rel, err := h.svc.CreateRelationship(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, rel)
	return nil
}

// DeleteRelationship godoc.
func (h *Handler) DeleteRelationship(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	if err := h.svc.DeleteRelationship(r.Context(), id); err != nil {
		return err // svc returns apperr.NotFound when missing
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// DeriveFromOrigin exposes the server-side kinship derivation (replaces frontend BFS).
func (h *Handler) DeriveFromOrigin(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("invalid id", "id")
	}
	derived, err := h.svc.DeriveFromOrigin(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, derived)
	return nil
}
