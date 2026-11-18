package chat

import (
	"net/http"

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

// currentAppUserID resolves the authenticated caller's id — chat is the first module scoped per
// app user rather than shared across the whole household. By the time RequirePerm has let a
// request through to this handler, AuthMiddleware has already populated the context, so the
// !ok branch here is unreachable in practice; it's kept because this handler shouldn't silently
// trust an invariant enforced by a different package.
func currentAppUserID(r *http.Request) (httpx.User, error) {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return httpx.User{}, apperr.Unauthorized("no authenticated user in context")
	}
	return u, nil
}

// List handles GET /api/chat/messages — the current app user's conversation, oldest first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	u, err := currentAppUserID(r)
	if err != nil {
		return err
	}

	dtos, err := h.service.List(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Create handles POST /api/chat/messages.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	u, err := currentAppUserID(r)
	if err != nil {
		return err
	}

	req, err := httpx.DecodeAndValidate[CreateMessageRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Create(r.Context(), u.ID, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// DeleteAll handles DELETE /api/chat/messages — clears the current app user's entire conversation.
func (h *Handler) DeleteAll(w http.ResponseWriter, r *http.Request) error {
	u, err := currentAppUserID(r)
	if err != nil {
		return err
	}

	if err := h.service.DeleteAll(r.Context(), u.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
