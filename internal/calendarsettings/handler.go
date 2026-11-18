package calendarsettings

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// targetResponse is the JSON shape for GET/PUT.
type targetResponse struct {
	Target Target `json:"target"`
}

// setTargetRequest is the PUT body.
type setTargetRequest struct {
	Target Target `json:"target" validate:"required,oneof=NONE ICLOUD GOOGLE"`
}

// Get handles GET /api/calendar/sync-target.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	t, err := h.service.Get(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, targetResponse{Target: t})
	return nil
}

// Set handles PUT /api/calendar/sync-target.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[setTargetRequest](r)
	if err != nil {
		return err
	}
	if err := h.service.Set(r.Context(), req.Target); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, targetResponse{Target: req.Target})
	return nil
}
