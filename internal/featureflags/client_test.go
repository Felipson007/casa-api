package featureflags

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientDisabledWithoutBaseURL(t *testing.T) {
	client := NewHTTPClient("")
	if client.Enabled() {
		t.Fatal("expected Enabled() false for empty base URL")
	}
	_, err := client.ListApplications(t.Context())
	var statusErr *StatusError
	if err == nil {
		t.Fatal("expected an error when the client is disabled")
	}
	if !asStatusError(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 StatusError, got %v", err)
	}
}

// asStatusError is errors.As, kept local to avoid importing "errors" just for one call site here.
func asStatusError(err error, target **StatusError) bool {
	se, ok := err.(*StatusError)
	if ok {
		*target = se
	}
	return ok
}

func TestHTTPClientHierarchyFlowMatchesControlPlaneContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/applications", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"applications": []Application{{ID: 1, Name: "atlas"}}})
	})
	mux.HandleFunc("POST /v1/applications", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, http.StatusCreated, Application{ID: 1, Name: body["name"]})
	})
	mux.HandleFunc("GET /v1/applications/atlas/environments/production/configurations/mobile-menu", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, ConfigurationSnapshot{
			Application: "atlas", Environment: "production", Configuration: "mobile-menu",
			Revision: 3, Flags: []Flag{{Key: "new-ui", Enabled: true, Revision: 3}},
		})
	})
	mux.HandleFunc("POST /v1/applications/atlas/environments/production/configurations/mobile-menu/flags", func(w http.ResponseWriter, r *http.Request) {
		var req CreateFlagRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, http.StatusCreated, Flag{Key: req.Key, Enabled: true, Revision: 1})
	})
	mux.HandleFunc("PATCH /v1/applications/atlas/environments/production/configurations/mobile-menu/flags/new-ui", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, http.StatusOK, Flag{Key: "new-ui", Enabled: body["enabled"], Revision: 4})
	})
	mux.HandleFunc("DELETE /v1/applications/atlas/environments/production/configurations/mobile-menu/flags/new-ui", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/applications/atlas/environments/production/configurations/missing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.HandleFunc("GET /internal/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, ControlPlaneStatus{Status: "UP", Revision: 5, Synchronized: true})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewHTTPClient(server.URL)
	ctx := t.Context()

	if !client.Enabled() {
		t.Fatal("expected Enabled() true with a base URL set")
	}

	apps, err := client.ListApplications(ctx)
	if err != nil || len(apps) != 1 || apps[0].Name != "atlas" {
		t.Fatalf("ListApplications: apps=%+v err=%v", apps, err)
	}

	app, err := client.CreateApplication(ctx, "atlas")
	if err != nil || app.Name != "atlas" {
		t.Fatalf("CreateApplication: app=%+v err=%v", app, err)
	}

	snapshot, err := client.GetConfiguration(ctx, "atlas", "production", "mobile-menu")
	if err != nil || snapshot.Revision != 3 || len(snapshot.Flags) != 1 {
		t.Fatalf("GetConfiguration: snapshot=%+v err=%v", snapshot, err)
	}

	flag, err := client.CreateFlag(ctx, "atlas", "production", "mobile-menu", CreateFlagRequest{Key: "new-ui"})
	if err != nil || flag.Key != "new-ui" {
		t.Fatalf("CreateFlag: flag=%+v err=%v", flag, err)
	}

	toggled, err := client.SetFlagEnabled(ctx, "atlas", "production", "mobile-menu", "new-ui", false)
	if err != nil || toggled.Enabled {
		t.Fatalf("SetFlagEnabled: flag=%+v err=%v", toggled, err)
	}

	if err := client.DeleteFlag(ctx, "atlas", "production", "mobile-menu", "new-ui"); err != nil {
		t.Fatalf("DeleteFlag: %v", err)
	}

	status, err := client.Status(ctx)
	if err != nil || status.Revision != 5 || !status.Synchronized {
		t.Fatalf("Status: status=%+v err=%v", status, err)
	}

	_, err = client.GetConfiguration(ctx, "atlas", "production", "missing")
	var statusErr *StatusError
	if !asStatusError(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound || statusErr.Message != "not found" {
		t.Fatalf("expected a 404 StatusError with the control plane's message, got %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
