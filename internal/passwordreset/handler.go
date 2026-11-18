package passwordreset

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RequestReset handles POST /api/auth/forgot-password. Always responds with the same generic
// message regardless of whether the email matched an account — the one place this module must
// NOT distinguish "found" from "not found" in its response, so the API can't be used to probe
// which emails have an account. A real infrastructure failure (DB error, etc.) still surfaces
// normally through httpx.Wrap.
func (h *Handler) RequestReset(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[RequestResetRequest](r)
	if err != nil {
		return err
	}

	if _, err := h.service.RequestReset(r.Context(), req.Email); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "If that email has an account, a password reset link has been generated.",
	})
	return nil
}

// ResetPassword handles POST /api/auth/reset-password. No RequirePerm here (see routes.go) — the
// token itself is the proof of identity; there is no authenticated user to check permissions for.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[ResetPasswordRequest](r)
	if err != nil {
		return err
	}

	if err := h.service.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
