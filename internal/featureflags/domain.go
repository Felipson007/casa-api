// Package featureflags is a thin administrative proxy in front of the feature-flag-mvp Control
// Plane (github.com/richardnogueira01/feature-flag-mvp — a separate product, not built for
// Atlas exclusively; Atlas is just its first real consumer, per task 020 of that repo's own
// backlog). It never lets the browser talk to the Control Plane or its PostgreSQL directly: the
// frontend calls same-origin /api/feature-flags/**, authenticated the same way as every other
// Atlas route, and this module forwards to FEATURE_FLAG_CONTROL_PLANE_URL.
//
// The runtime evaluation Agent is intentionally out of scope here — this module only exercises
// the Control Plane's administrative API (create/list/edit the application/environment/
// configuration/flag hierarchy), never the Agent's near-cache.
package featureflags

import (
	"context"
	"encoding/json"
)

// Application, Environment, Configuration and ScopedFlag mirror the Control Plane's own JSON
// shapes (see feature-flag-mvp's internal/control/scope.go) with the field names translated to
// this codebase's camelCase DTO convention.
type Application struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Environment struct {
	ID            int64  `json:"id"`
	ApplicationID int64  `json:"applicationId"`
	Name          string `json:"name"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type Configuration struct {
	ID            int64  `json:"id"`
	EnvironmentID int64  `json:"environmentId"`
	Name          string `json:"name"`
	Revision      uint64 `json:"revision"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// Flag is a flag scoped to one configuration. Value carries whatever JSON the Control Plane
// stored — a boolean, number, string, object or array (see feature-flag-mvp's "flexible flag
// values" history entry).
type Flag struct {
	Key      string          `json:"key"`
	Enabled  bool            `json:"enabled"`
	Value    json.RawMessage `json:"value,omitempty"`
	Revision uint64          `json:"revision"`
}

// ConfigurationSnapshot is the bulk view of one configuration: every flag it holds, plus the
// configuration's own monotonic revision and last-write timestamp.
type ConfigurationSnapshot struct {
	Application   string `json:"application"`
	Environment   string `json:"environment"`
	Configuration string `json:"configuration"`
	Revision      uint64 `json:"revision"`
	UpdatedAt     string `json:"updatedAt"`
	Flags         []Flag `json:"flags"`
}

// ControlPlaneStatus mirrors the Control Plane's GET /internal/status — process-wide, not
// per-configuration (the Control Plane does not track status at that granularity yet; scoped
// Agent status is feature-flag-mvp's task 014, not implemented).
type ControlPlaneStatus struct {
	Status       string `json:"status"`
	Revision     uint64 `json:"revision"`
	Synchronized bool   `json:"synchronized"`
}

// CreateFlagRequest is the JSON body accepted by POST .../flags. Enabled and Value are passed
// through to the Control Plane as given; it is the one source of truth for the "exactly one of
// value or enabled" rule, so this proxy does not duplicate that validation.
type CreateFlagRequest struct {
	Key     string          `json:"key" validate:"required,min=1,max=128"`
	Enabled *bool           `json:"enabled,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// UpdateFlagRequest is the JSON body accepted by PUT .../flags/{key}.
type UpdateFlagRequest struct {
	Enabled *bool           `json:"enabled,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// PatchFlagRequest is the JSON body accepted by PATCH .../flags/{key} (toggle only, value
// untouched). Enabled is a pointer so an explicit `false` is distinguishable from "not sent" —
// validator's "required" on a plain bool would wrongly reject `{"enabled": false}`.
type PatchFlagRequest struct {
	Enabled *bool `json:"enabled" validate:"required"`
}

// NameRequest is the JSON body accepted by every "create application/environment/configuration"
// endpoint — they all just take a name.
type NameRequest struct {
	Name string `json:"name" validate:"required,min=1,max=128"`
}

// Client is what Service depends on to reach the Control Plane — implemented by *HTTPClient in
// production and a fake in tests, so handler/service tests never need a real feature-flag-mvp
// instance running.
type Client interface {
	Enabled() bool

	ListApplications(ctx context.Context) ([]Application, error)
	CreateApplication(ctx context.Context, name string) (Application, error)

	ListEnvironments(ctx context.Context, application string) ([]Environment, error)
	CreateEnvironment(ctx context.Context, application, name string) (Environment, error)

	ListConfigurations(ctx context.Context, application, environment string) ([]Configuration, error)
	CreateConfiguration(ctx context.Context, application, environment, name string) (Configuration, error)
	GetConfiguration(ctx context.Context, application, environment, configuration string) (ConfigurationSnapshot, error)

	CreateFlag(ctx context.Context, application, environment, configuration string, req CreateFlagRequest) (Flag, error)
	GetFlag(ctx context.Context, application, environment, configuration, key string) (Flag, error)
	UpdateFlag(ctx context.Context, application, environment, configuration, key string, req UpdateFlagRequest) (Flag, error)
	SetFlagEnabled(ctx context.Context, application, environment, configuration, key string, enabled bool) (Flag, error)
	DeleteFlag(ctx context.Context, application, environment, configuration, key string) error

	Status(ctx context.Context) (ControlPlaneStatus, error)
}
