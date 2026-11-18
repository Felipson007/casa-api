package integrations

import (
	"net/http"
	"net/url"

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

// googleDriveStateCookie holds the OAuth CSRF state across the redirect to Google and back — a
// short-lived cookie instead of a server-side session (this app has no generic session store; its
// `sessions` table is specifically the login mechanism — see googledrive_oauth.go's doc comment).
const googleDriveStateCookie = "google_drive_oauth_state"

// ConnectGoogleDrive handles GET /api/integrations/google-drive/connect — starts the OAuth flow by
// redirecting the browser to Google's consent screen.
func (h *Handler) ConnectGoogleDrive(w http.ResponseWriter, r *http.Request) error {
	state, authURL, err := h.service.BeginGoogleDriveConnect()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: googleDriveStateCookie, Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isRequestSecure(r), MaxAge: 600,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// GoogleDriveCallback handles GET /api/integrations/google-drive/callback — called by the browser,
// redirected by Google, never by the frontend via fetch; responds with another redirect (to
// /configuracoes) rather than JSON, mirroring IntegracaoExternaController.callback's redirect-based
// contract (driveTenantConectado=1/driveTenantErro=... query params, which the existing frontend
// settings page already reads — those param names stay as-is, only the route path changed).
func (h *Handler) GoogleDriveCallback(w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, &http.Cookie{Name: googleDriveStateCookie, Value: "", Path: "/", MaxAge: -1})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectToSettings(w, r, "driveTenantErro", errParam)
		return nil
	}

	cookie, cookieErr := r.Cookie(googleDriveStateCookie)
	state := r.URL.Query().Get("state")
	if cookieErr != nil || cookie.Value == "" || state == "" || cookie.Value != state {
		h.redirectToSettings(w, r, "driveTenantErro", "Fluxo de autorização inválido ou expirado. Tente conectar o Google Drive novamente.")
		return nil
	}

	if _, err := h.service.CompleteGoogleDriveConnect(r.Context(), r.URL.Query().Get("code")); err != nil {
		h.redirectToSettings(w, r, "driveTenantErro", err.Error())
		return nil
	}
	h.redirectToSettings(w, r, "driveTenantConectado", "1")
	return nil
}

func (h *Handler) redirectToSettings(w http.ResponseWriter, r *http.Request, param, value string) {
	dest := h.frontendOrigin + "/configuracoes?" + param + "=" + url.QueryEscape(value)
	http.Redirect(w, r, dest, http.StatusFound)
}

// isRequestSecure mirrors auth's own helper — Secure whenever the request arrived over TLS,
// directly or via a reverse proxy that terminates it (X-Forwarded-Proto).
func isRequestSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// ListExternal handles GET /api/integrations/external.
func (h *Handler) ListExternal(w http.ResponseWriter, r *http.Request) error {
	dtos, err := h.service.ListExternal(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Connect handles POST /api/integrations/external/connect.
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[ConnectExternalIntegrationRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Connect(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// DisconnectExternal handles DELETE /api/integrations/external/{type}.
func (h *Handler) DisconnectExternal(w http.ResponseWriter, r *http.Request) error {
	t := Type(r.PathValue("type"))
	if err := h.service.DisconnectExternal(r.Context(), t); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// SystemStatus handles GET /api/integrations/system — 200 with a `null` body if nothing is
// connected (mirrors the old Java Optional<IntegracaoSistemaDto>).
func (h *Handler) SystemStatus(w http.ResponseWriter, r *http.Request) error {
	dto, err := h.service.SystemStatus(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// DisconnectSystem handles DELETE /api/integrations/system/{type}.
func (h *Handler) DisconnectSystem(w http.ResponseWriter, r *http.Request) error {
	t := Type(r.PathValue("type"))
	if err := h.service.DisconnectSystem(r.Context(), t); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
