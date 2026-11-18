package featureflags

import (
	"context"
	"errors"
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Service sits between Handler and Client, translating the Control Plane's HTTP status codes
// into this codebase's apperr.Kind taxonomy — the one place that mapping happens, mirroring how
// every other module's handler.go maps its own Repository errors via errors.Is.
type Service struct{ client Client }

func NewService(client Client) *Service { return &Service{client: client} }

// Enabled reports whether FEATURE_FLAG_CONTROL_PLANE_URL was configured.
func (s *Service) Enabled() bool { return s.client.Enabled() }

// mapErr turns a *StatusError from the Control Plane into the matching apperr.Kind, and anything
// else (network failure, decode failure, an unmapped status code) into apperr.Internal — so
// Service always returns nil or a properly-typed *apperr.Error, never a bare error.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		// Not a StatusError — a network/decode failure talking to the Control Plane. Wrap it here
		// rather than leaving it to httpx.Wrap's own fallback, so Service always returns either
		// nil or a properly-typed *apperr.Error to any caller, not just HTTP handlers.
		return apperr.Internal(err)
	}
	switch statusErr.StatusCode {
	case http.StatusNotFound:
		return apperr.NotFound(statusErr.Message)
	case http.StatusConflict:
		return apperr.Conflict(statusErr.Message)
	case http.StatusBadRequest:
		return apperr.Validation(statusErr.Message)
	default:
		return apperr.Internal(err)
	}
}

func (s *Service) ListApplications(ctx context.Context) ([]Application, error) {
	apps, err := s.client.ListApplications(ctx)
	return apps, mapErr(err)
}

func (s *Service) CreateApplication(ctx context.Context, name string) (Application, error) {
	app, err := s.client.CreateApplication(ctx, name)
	return app, mapErr(err)
}

func (s *Service) ListEnvironments(ctx context.Context, application string) ([]Environment, error) {
	envs, err := s.client.ListEnvironments(ctx, application)
	return envs, mapErr(err)
}

func (s *Service) CreateEnvironment(ctx context.Context, application, name string) (Environment, error) {
	env, err := s.client.CreateEnvironment(ctx, application, name)
	return env, mapErr(err)
}

func (s *Service) ListConfigurations(ctx context.Context, application, environment string) ([]Configuration, error) {
	cfgs, err := s.client.ListConfigurations(ctx, application, environment)
	return cfgs, mapErr(err)
}

func (s *Service) CreateConfiguration(ctx context.Context, application, environment, name string) (Configuration, error) {
	cfg, err := s.client.CreateConfiguration(ctx, application, environment, name)
	return cfg, mapErr(err)
}

func (s *Service) GetConfiguration(ctx context.Context, application, environment, configuration string) (ConfigurationSnapshot, error) {
	snapshot, err := s.client.GetConfiguration(ctx, application, environment, configuration)
	return snapshot, mapErr(err)
}

func (s *Service) CreateFlag(ctx context.Context, application, environment, configuration string, req CreateFlagRequest) (Flag, error) {
	flag, err := s.client.CreateFlag(ctx, application, environment, configuration, req)
	return flag, mapErr(err)
}

func (s *Service) GetFlag(ctx context.Context, application, environment, configuration, key string) (Flag, error) {
	flag, err := s.client.GetFlag(ctx, application, environment, configuration, key)
	return flag, mapErr(err)
}

func (s *Service) UpdateFlag(ctx context.Context, application, environment, configuration, key string, req UpdateFlagRequest) (Flag, error) {
	flag, err := s.client.UpdateFlag(ctx, application, environment, configuration, key, req)
	return flag, mapErr(err)
}

func (s *Service) SetFlagEnabled(ctx context.Context, application, environment, configuration, key string, enabled bool) (Flag, error) {
	flag, err := s.client.SetFlagEnabled(ctx, application, environment, configuration, key, enabled)
	return flag, mapErr(err)
}

func (s *Service) DeleteFlag(ctx context.Context, application, environment, configuration, key string) error {
	return mapErr(s.client.DeleteFlag(ctx, application, environment, configuration, key))
}

func (s *Service) Status(ctx context.Context) (ControlPlaneStatus, error) {
	status, err := s.client.Status(ctx)
	return status, mapErr(err)
}
