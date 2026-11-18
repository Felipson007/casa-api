package featureflags

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StatusError is returned for a non-2xx response from the Control Plane, carrying enough for the
// handler to map it to the right apperr.Kind without string-matching the message.
type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("control plane returned %d: %s", e.StatusCode, e.Message)
}

// HTTPClient is the real Client implementation, talking to a feature-flag-mvp Control Plane over
// plain HTTP — no API key: the Control Plane has no auth of its own yet (see its ARCHITECTURE.md,
// "Security — Future Decision"), so this trusts the internal network the way every other
// service-to-service call in this deployment does.
type HTTPClient struct {
	baseURL string
	http    *http.Client
}

// NewHTTPClient builds a client for baseURL (FEATURE_FLAG_CONTROL_PLANE_URL). An empty baseURL is
// allowed — Enabled() reports false and every call fails fast with a clear error, so a deployment
// without the feature-flag service configured never crashes Atlas itself.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *HTTPClient) Enabled() bool { return c != nil && c.baseURL != "" }

func (c *HTTPClient) do(ctx context.Context, method, path string, body any, out any) error {
	if !c.Enabled() {
		return &StatusError{StatusCode: http.StatusNotFound, Message: "feature flag control plane is not configured"}
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Never include req.URL in the wrapped error at Info/Warn level beyond this — the caller
		// (Service) logs via httpx.Wrap, which logs err.Error() but never request/response bodies,
		// so no data from the Control Plane (which has no secrets to begin with) leaks either way.
		return fmt.Errorf("call control plane: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read control plane response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := string(raw)
		var problem struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &problem) == nil && problem.Error != "" {
			message = problem.Error
		}
		return &StatusError{StatusCode: resp.StatusCode, Message: message}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode control plane response: %w", err)
	}
	return nil
}

func scopePath(application, environment, configuration string) string {
	return "/v1/applications/" + url.PathEscape(application) +
		"/environments/" + url.PathEscape(environment) +
		"/configurations/" + url.PathEscape(configuration)
}

func (c *HTTPClient) ListApplications(ctx context.Context) ([]Application, error) {
	var out struct {
		Applications []Application `json:"applications"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/applications", nil, &out); err != nil {
		return nil, err
	}
	return out.Applications, nil
}

func (c *HTTPClient) CreateApplication(ctx context.Context, name string) (Application, error) {
	var out Application
	err := c.do(ctx, http.MethodPost, "/v1/applications", map[string]string{"name": name}, &out)
	return out, err
}

func (c *HTTPClient) ListEnvironments(ctx context.Context, application string) ([]Environment, error) {
	var out struct {
		Environments []Environment `json:"environments"`
	}
	path := "/v1/applications/" + url.PathEscape(application) + "/environments"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Environments, nil
}

func (c *HTTPClient) CreateEnvironment(ctx context.Context, application, name string) (Environment, error) {
	var out Environment
	path := "/v1/applications/" + url.PathEscape(application) + "/environments"
	err := c.do(ctx, http.MethodPost, path, map[string]string{"name": name}, &out)
	return out, err
}

func (c *HTTPClient) ListConfigurations(ctx context.Context, application, environment string) ([]Configuration, error) {
	var out struct {
		Configurations []Configuration `json:"configurations"`
	}
	path := "/v1/applications/" + url.PathEscape(application) + "/environments/" + url.PathEscape(environment) + "/configurations"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Configurations, nil
}

func (c *HTTPClient) CreateConfiguration(ctx context.Context, application, environment, name string) (Configuration, error) {
	var out Configuration
	path := "/v1/applications/" + url.PathEscape(application) + "/environments/" + url.PathEscape(environment) + "/configurations"
	err := c.do(ctx, http.MethodPost, path, map[string]string{"name": name}, &out)
	return out, err
}

func (c *HTTPClient) GetConfiguration(ctx context.Context, application, environment, configuration string) (ConfigurationSnapshot, error) {
	var out ConfigurationSnapshot
	err := c.do(ctx, http.MethodGet, scopePath(application, environment, configuration), nil, &out)
	return out, err
}

func (c *HTTPClient) CreateFlag(ctx context.Context, application, environment, configuration string, req CreateFlagRequest) (Flag, error) {
	var out Flag
	err := c.do(ctx, http.MethodPost, scopePath(application, environment, configuration)+"/flags", req, &out)
	return out, err
}

func (c *HTTPClient) GetFlag(ctx context.Context, application, environment, configuration, key string) (Flag, error) {
	var out Flag
	path := scopePath(application, environment, configuration) + "/flags/" + url.PathEscape(key)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *HTTPClient) UpdateFlag(ctx context.Context, application, environment, configuration, key string, req UpdateFlagRequest) (Flag, error) {
	var out Flag
	path := scopePath(application, environment, configuration) + "/flags/" + url.PathEscape(key)
	err := c.do(ctx, http.MethodPut, path, req, &out)
	return out, err
}

func (c *HTTPClient) SetFlagEnabled(ctx context.Context, application, environment, configuration, key string, enabled bool) (Flag, error) {
	var out Flag
	path := scopePath(application, environment, configuration) + "/flags/" + url.PathEscape(key)
	err := c.do(ctx, http.MethodPatch, path, map[string]bool{"enabled": enabled}, &out)
	return out, err
}

func (c *HTTPClient) DeleteFlag(ctx context.Context, application, environment, configuration, key string) error {
	path := scopePath(application, environment, configuration) + "/flags/" + url.PathEscape(key)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) Status(ctx context.Context) (ControlPlaneStatus, error) {
	var out ControlPlaneStatus
	err := c.do(ctx, http.MethodGet, "/internal/status", nil, &out)
	return out, err
}

var _ Client = (*HTTPClient)(nil)
