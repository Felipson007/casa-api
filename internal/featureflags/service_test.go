package featureflags

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeClient is an in-memory Client used by unit tests — no Control Plane HTTP call involved.
type fakeClient struct {
	enabled bool
	apps    []Application
	err     error
}

func (f *fakeClient) Enabled() bool { return f.enabled }
func (f *fakeClient) ListApplications(context.Context) ([]Application, error) {
	return f.apps, f.err
}
func (f *fakeClient) CreateApplication(_ context.Context, name string) (Application, error) {
	if f.err != nil {
		return Application{}, f.err
	}
	return Application{Name: name}, nil
}
func (f *fakeClient) ListEnvironments(context.Context, string) ([]Environment, error) {
	return nil, f.err
}
func (f *fakeClient) CreateEnvironment(_ context.Context, _, name string) (Environment, error) {
	return Environment{Name: name}, f.err
}
func (f *fakeClient) ListConfigurations(context.Context, string, string) ([]Configuration, error) {
	return nil, f.err
}
func (f *fakeClient) CreateConfiguration(_ context.Context, _, _, name string) (Configuration, error) {
	return Configuration{Name: name}, f.err
}
func (f *fakeClient) GetConfiguration(context.Context, string, string, string) (ConfigurationSnapshot, error) {
	return ConfigurationSnapshot{}, f.err
}
func (f *fakeClient) CreateFlag(_ context.Context, _, _, _ string, req CreateFlagRequest) (Flag, error) {
	return Flag{Key: req.Key}, f.err
}
func (f *fakeClient) GetFlag(context.Context, string, string, string, string) (Flag, error) {
	return Flag{}, f.err
}
func (f *fakeClient) UpdateFlag(context.Context, string, string, string, string, UpdateFlagRequest) (Flag, error) {
	return Flag{}, f.err
}
func (f *fakeClient) SetFlagEnabled(_ context.Context, _, _, _, key string, enabled bool) (Flag, error) {
	return Flag{Key: key, Enabled: enabled}, f.err
}
func (f *fakeClient) DeleteFlag(context.Context, string, string, string, string) error { return f.err }
func (f *fakeClient) Status(context.Context) (ControlPlaneStatus, error) {
	return ControlPlaneStatus{}, f.err
}

var _ Client = (*fakeClient)(nil)

func TestServiceEnabledPassesThrough(t *testing.T) {
	svc := NewService(&fakeClient{enabled: true})
	if !svc.Enabled() {
		t.Fatal("expected Enabled() true")
	}
	svc2 := NewService(&fakeClient{enabled: false})
	if svc2.Enabled() {
		t.Fatal("expected Enabled() false")
	}
}

func TestServiceMapsControlPlaneStatusCodesToApperrKinds(t *testing.T) {
	cases := []struct {
		status int
		want   apperr.Kind
	}{
		{http.StatusNotFound, apperr.KindNotFound},
		{http.StatusConflict, apperr.KindConflict},
		{http.StatusBadRequest, apperr.KindValidation},
		{http.StatusInternalServerError, apperr.KindInternal},
	}
	for _, c := range cases {
		client := &fakeClient{enabled: true, err: &StatusError{StatusCode: c.status, Message: "boom"}}
		svc := NewService(client)
		_, err := svc.CreateApplication(context.Background(), "atlas")
		var appErr *apperr.Error
		if !errors.As(err, &appErr) {
			t.Fatalf("status %d: expected *apperr.Error, got %v", c.status, err)
		}
		if appErr.Kind != c.want {
			t.Fatalf("status %d: expected kind %v, got %v", c.status, c.want, appErr.Kind)
		}
	}
}

func TestServiceReturnsDataOnSuccess(t *testing.T) {
	client := &fakeClient{enabled: true, apps: []Application{{Name: "atlas"}}}
	svc := NewService(client)
	apps, err := svc.ListApplications(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "atlas" {
		t.Fatalf("got %+v", apps)
	}
}

func TestServiceWrapsNetworkErrorsAsInternal(t *testing.T) {
	client := &fakeClient{enabled: true, err: errors.New("dial tcp: connection refused")}
	svc := NewService(client)
	_, err := svc.Status(context.Background())
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindInternal {
		t.Fatalf("expected apperr.KindInternal, got %v", err)
	}
}

func TestStatusErrorMessage(t *testing.T) {
	err := &StatusError{StatusCode: 404, Message: "not found"}
	if err.Error() != "control plane returned 404: not found" {
		t.Fatalf("unexpected message: %q", err.Error())
	}
}
