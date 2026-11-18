package profiling

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandlerExposesProfiles(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/goroutine"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
	}
}
