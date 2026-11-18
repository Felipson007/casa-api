package featureflags

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

func ownerContext() context.Context {
	return httpx.WithUser(context.Background(), httpx.User{ID: uuid.New(), Role: "OWNER"})
}

func TestHandlerReturns404WhenNotConfigured(t *testing.T) {
	handler := NewHandler(NewService(&fakeClient{enabled: false}))
	r := httptest.NewRequest(http.MethodGet, "/api/feature-flags/applications", nil).WithContext(ownerContext())
	w := httptest.NewRecorder()
	httpx.Wrap(handler.ListApplications).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerListApplications(t *testing.T) {
	handler := NewHandler(NewService(&fakeClient{enabled: true, apps: []Application{{ID: 1, Name: "atlas"}}}))
	r := httptest.NewRequest(http.MethodGet, "/api/feature-flags/applications", nil).WithContext(ownerContext())
	w := httptest.NewRecorder()
	httpx.Wrap(handler.ListApplications).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Applications []Application `json:"applications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Applications) != 1 || body.Applications[0].Name != "atlas" {
		t.Fatalf("got %+v", body.Applications)
	}
}

func TestHandlerCreateApplicationValidatesBody(t *testing.T) {
	handler := NewHandler(NewService(&fakeClient{enabled: true}))
	r := httptest.NewRequest(http.MethodPost, "/api/feature-flags/applications", strings.NewReader(`{"name":""}`)).WithContext(ownerContext())
	w := httptest.NewRecorder()
	httpx.Wrap(handler.CreateApplication).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an empty name, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerPatchFlagRejectsMissingEnabled(t *testing.T) {
	handler := NewHandler(NewService(&fakeClient{enabled: true}))
	r := httptest.NewRequest(http.MethodPatch, "/api/feature-flags/applications/atlas/environments/production/configurations/mobile-menu/flags/new-ui", strings.NewReader(`{}`))
	r = r.WithContext(ownerContext())
	w := httptest.NewRecorder()
	httpx.Wrap(handler.PatchFlag).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when enabled is missing, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerRoundTripThroughRegisterRoutes(t *testing.T) {
	client := &fakeClient{enabled: true, apps: []Application{{Name: "atlas"}}}
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(NewService(client)))

	r := httptest.NewRequest(http.MethodGet, "/api/feature-flags/applications", nil).WithContext(ownerContext())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("OWNER should bypass RequirePerm: status=%d body=%s", w.Code, w.Body.String())
	}
}
