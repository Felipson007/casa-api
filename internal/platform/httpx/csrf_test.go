package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectCrossSiteMutations(t *testing.T) {
	const self = "https://atlas-server.taile22ede.ts.net"
	guard := RejectCrossSiteMutations(self)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(method, path string, headers map[string]string) int {
		r := httptest.NewRequest(method, "https://atlas-server.taile22ede.ts.net"+path, nil)
		r.TLS = &tls.ConnectionState{} // mark as https so requestOrigin() == self
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, r)
		return rec.Code
	}

	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{"GET is never checked", http.MethodGet, "/api/loans", map[string]string{"Origin": "https://evil.example"}, http.StatusOK},
		{"POST no Origin/Referer (bearer script)", http.MethodPost, "/api/loans", nil, http.StatusOK},
		{"POST same-origin via APP_FRONTEND_ORIGIN", http.MethodPost, "/api/loans", map[string]string{"Origin": self}, http.StatusOK},
		{"POST same-origin via request host", http.MethodPost, "/api/loans", map[string]string{"Origin": "https://atlas-server.taile22ede.ts.net"}, http.StatusOK},
		{"POST cross-site Origin", http.MethodPost, "/api/loans", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"DELETE cross-site Referer", http.MethodDelete, "/api/loans/1", map[string]string{"Referer": "https://evil.example/attack.html"}, http.StatusForbidden},
		{"PATCH cross-site Origin", http.MethodPatch, "/api/loans/1/due-day", map[string]string{"Origin": "http://localhost:9999"}, http.StatusForbidden},
		{"public route is exempt", http.MethodPost, "/api/public/dashboard-chat", map[string]string{"Origin": "https://evil.example"}, http.StatusOK},
	}
	for _, c := range cases {
		if got := call(c.method, c.path, c.headers); got != c.want {
			t.Errorf("%s: got status %d, want %d", c.name, got, c.want)
		}
	}
}
