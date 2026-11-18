package featureflags

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the feature-flag proxy endpoints on mux. Composition happens in
// cmd/api/main.go (D-005). Gated by httpx.RequirePerm (D-003), module "feature-flags" — same
// idiom as every other module (see internal/settings/routes.go for the reference pattern). The
// runtime evaluation Agent is never exposed here — only the Control Plane's administrative API.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "feature-flags"

	mux.HandleFunc("GET /api/feature-flags/applications", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListApplications)))
	mux.HandleFunc("POST /api/feature-flags/applications", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateApplication)))

	mux.HandleFunc("GET /api/feature-flags/applications/{application}/environments", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListEnvironments)))
	mux.HandleFunc("POST /api/feature-flags/applications/{application}/environments", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateEnvironment)))

	mux.HandleFunc("GET /api/feature-flags/applications/{application}/environments/{environment}/configurations", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListConfigurations)))
	mux.HandleFunc("POST /api/feature-flags/applications/{application}/environments/{environment}/configurations", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateConfiguration)))
	mux.HandleFunc("GET /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetConfiguration)))

	mux.HandleFunc("POST /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}/flags", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.CreateFlag)))
	mux.HandleFunc("GET /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}/flags/{key}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetFlag)))
	mux.HandleFunc("PUT /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}/flags/{key}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.UpdateFlag)))
	mux.HandleFunc("PATCH /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}/flags/{key}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.PatchFlag)))
	mux.HandleFunc("DELETE /api/feature-flags/applications/{application}/environments/{environment}/configurations/{configuration}/flags/{key}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteFlag)))

	mux.HandleFunc("GET /api/feature-flags/status", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Status)))
}
