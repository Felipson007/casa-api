package featureflags

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP surface for the feature-flag Control Plane proxy.
type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// requireEnabled short-circuits every handler with a clear message when
// FEATURE_FLAG_CONTROL_PLANE_URL is not set, instead of letting each one hit the Control Plane
// and get a generic connection error.
func (h *Handler) requireEnabled() error {
	if !h.service.Enabled() {
		return apperr.NotFound("feature flag control plane is not configured (FEATURE_FLAG_CONTROL_PLANE_URL)")
	}
	return nil
}

func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	apps, err := h.service.ListApplications(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applications": apps})
	return nil
}

func (h *Handler) CreateApplication(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[NameRequest](r)
	if err != nil {
		return err
	}
	app, err := h.service.CreateApplication(r.Context(), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, app)
	return nil
}

func (h *Handler) ListEnvironments(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	envs, err := h.service.ListEnvironments(r.Context(), r.PathValue("application"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"environments": envs})
	return nil
}

func (h *Handler) CreateEnvironment(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[NameRequest](r)
	if err != nil {
		return err
	}
	env, err := h.service.CreateEnvironment(r.Context(), r.PathValue("application"), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, env)
	return nil
}

func (h *Handler) ListConfigurations(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	cfgs, err := h.service.ListConfigurations(r.Context(), r.PathValue("application"), r.PathValue("environment"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"configurations": cfgs})
	return nil
}

func (h *Handler) CreateConfiguration(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[NameRequest](r)
	if err != nil {
		return err
	}
	cfg, err := h.service.CreateConfiguration(r.Context(), r.PathValue("application"), r.PathValue("environment"), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, cfg)
	return nil
}

func (h *Handler) GetConfiguration(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	snapshot, err := h.service.GetConfiguration(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, snapshot)
	return nil
}

func (h *Handler) CreateFlag(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[CreateFlagRequest](r)
	if err != nil {
		return err
	}
	flag, err := h.service.CreateFlag(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, flag)
	return nil
}

func (h *Handler) GetFlag(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	flag, err := h.service.GetFlag(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"), r.PathValue("key"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, flag)
	return nil
}

func (h *Handler) UpdateFlag(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[UpdateFlagRequest](r)
	if err != nil {
		return err
	}
	flag, err := h.service.UpdateFlag(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"), r.PathValue("key"), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, flag)
	return nil
}

func (h *Handler) PatchFlag(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	req, err := httpx.DecodeAndValidate[PatchFlagRequest](r)
	if err != nil {
		return err
	}
	flag, err := h.service.SetFlagEnabled(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"), r.PathValue("key"), *req.Enabled)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, flag)
	return nil
}

func (h *Handler) DeleteFlag(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	if err := h.service.DeleteFlag(r.Context(), r.PathValue("application"), r.PathValue("environment"), r.PathValue("configuration"), r.PathValue("key")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireEnabled(); err != nil {
		return err
	}
	status, err := h.service.Status(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, status)
	return nil
}
