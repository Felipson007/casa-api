package googlecal

import (
	"net/http"
	"net/url"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service        *Service
	frontendOrigin string
}

func NewHandler(service *Service, frontendOrigin string) *Handler {
	return &Handler{service: service, frontendOrigin: frontendOrigin}
}

const googleCalendarStateCookie = "google_calendar_oauth_state"

// ConnectGoogleCalendar handles GET /api/integrations/google-calendar/connect — redirects the
// browser to Google's consent screen (mirrors the Drive connect flow).
func (h *Handler) ConnectGoogleCalendar(w http.ResponseWriter, r *http.Request) error {
	state, authURL, err := h.service.BeginConnect()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: googleCalendarStateCookie, Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isRequestSecure(r), MaxAge: 600,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// GoogleCalendarCallback handles GET /api/integrations/google-calendar/callback.
func (h *Handler) GoogleCalendarCallback(w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, &http.Cookie{Name: googleCalendarStateCookie, Value: "", Path: "/", MaxAge: -1})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectToSettings(w, r, "googleCalendarErro", errParam)
		return nil
	}
	cookie, cookieErr := r.Cookie(googleCalendarStateCookie)
	state := r.URL.Query().Get("state")
	if cookieErr != nil || cookie.Value == "" || state == "" || cookie.Value != state {
		h.redirectToSettings(w, r, "googleCalendarErro", "Fluxo de autorização inválido ou expirado. Tente conectar de novo.")
		return nil
	}
	if _, err := h.service.CompleteConnect(r.Context(), r.URL.Query().Get("code")); err != nil {
		h.redirectToSettings(w, r, "googleCalendarErro", err.Error())
		return nil
	}
	h.redirectToSettings(w, r, "googleCalendarConectado", "1")
	return nil
}

func (h *Handler) redirectToSettings(w http.ResponseWriter, r *http.Request, param, value string) {
	dest := h.frontendOrigin + "/configuracoes?tab=conexoes&" + param + "=" + url.QueryEscape(value)
	http.Redirect(w, r, dest, http.StatusFound)
}

func isRequestSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// Connect handles PUT /api/integrations/google-calendar/connection (calendar selection).
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[integrations.ConnectGoogleCalendarRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Connect(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Status handles GET /api/integrations/google-calendar/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.Status(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}

// Calendars handles GET /api/integrations/google-calendar/calendars.
func (h *Handler) Calendars(w http.ResponseWriter, r *http.Request) error {
	cals, err := h.service.Calendars(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, cals)
	return nil
}

// SyncNow handles POST /api/integrations/google-calendar/sync.
func (h *Handler) SyncNow(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.SyncNow(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}
