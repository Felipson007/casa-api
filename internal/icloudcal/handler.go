package icloudcal

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// connectResponse returns the stored connection plus every calendar the account exposes, so the
// UI can render the picker in the same round trip.
type connectResponse struct {
	Integration integrations.ExternalIntegrationDTO `json:"integration"`
	Calendars   []CalendarOption                    `json:"calendars"`
}

// Connect handles PUT /api/integrations/icloud-calendar/connection.
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[integrations.ConnectICloudRequest](r)
	if err != nil {
		return err
	}
	dto, cals, err := h.service.Connect(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, connectResponse{Integration: dto, Calendars: cals})
	return nil
}

// Status handles GET /api/integrations/icloud-calendar/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.Status(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}

// Calendars handles GET /api/integrations/icloud-calendar/calendars.
func (h *Handler) Calendars(w http.ResponseWriter, r *http.Request) error {
	cals, err := h.service.Calendars(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, cals)
	return nil
}

// SyncNow handles POST /api/integrations/icloud-calendar/sync — runs a reconciliation immediately
// and returns what changed.
func (h *Handler) SyncNow(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.SyncNow(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}

// Probe handles POST /api/integrations/icloud-calendar/probe — structural diagnostics against the
// real CalDAV server (no names/ids), the CalDAV counterpart of cmd/pluggy-probe.
func (h *Handler) Probe(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.Probe(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}
