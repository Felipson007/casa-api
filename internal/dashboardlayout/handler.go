package dashboardlayout

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

// currentAppUserID resolves the authenticated caller's id — same idiom as internal/chat/handler.go,
// the other module scoped per app_user_id rather than shared across the household.
func currentAppUserID(r *http.Request) (httpx.User, error) {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return httpx.User{}, apperr.Unauthorized("no authenticated user in context")
	}
	return u, nil
}

// Get handles GET /api/dashboard/layout — 200 with a null layout when the current app user has
// never customized their dashboard.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	u, err := currentAppUserID(r)
	if err != nil {
		return err
	}

	dto, err := h.service.Get(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Set handles PUT /api/dashboard/layout — upserts the current app user's layout.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) error {
	u, err := currentAppUserID(r)
	if err != nil {
		return err
	}

	req, err := httpx.DecodeAndValidate[SetLayoutRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Set(r.Context(), u.ID, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}
