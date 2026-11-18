package integrations

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module
// "integrations" — see internal/expenses/routes.go for the reference pattern. No OAuth
// authorize/callback routes exist here — see the package doc for why.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "integrations"

	mux.HandleFunc("GET /api/integrations/external", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListExternal)))
	mux.HandleFunc("POST /api/integrations/external/connect", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Connect)))
	mux.HandleFunc("DELETE /api/integrations/external/{type}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DisconnectExternal)))

	// Google Drive OAuth connect/callback — real browser navigations (redirects), not fetch calls,
	// so these are gated by RequirePerm same as everything else here (session cookie already
	// carries the household's one user through), but respond with 302s, never JSON — see handler.go.
	// English paths per this rewrite's naming convention — unlike the login flow's callback (kept
	// at Spring's fixed /login/oauth2/code/google path), this one has no external framework
	// constraint forcing a specific path, just Google Cloud Console's authorized-redirect-URI list,
	// which needs updating to match: https://<frontend-origin>/api/integrations/google-drive/callback.
	mux.HandleFunc("GET /api/integrations/google-drive/connect", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.ConnectGoogleDrive)))
	mux.HandleFunc("GET /api/integrations/google-drive/callback", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.GoogleDriveCallback)))

	mux.HandleFunc("GET /api/integrations/system", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.SystemStatus)))
	mux.HandleFunc("DELETE /api/integrations/system/{type}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DisconnectSystem)))
}
