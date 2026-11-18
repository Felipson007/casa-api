package icloudcal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const principalBody = `<?xml version="1.0" encoding="utf-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/</href>
    <propstat>
      <prop>
        <current-user-principal><href>/18038948591/principal/</href></current-user-principal>
        <calendar-home-set xmlns="urn:ietf:params:xml:ns:caldav"><href xmlns="DAV:">https://p143-caldav.icloud.com/18038948591/calendars/</href></calendar-home-set>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`

// The two extractors must read their own property: iCloud returns current-user-principal (relative,
// no host) and calendar-home-set (absolute, on the shard) in the SAME propstat. Reading "the first
// href" would grab the relative principal and miss the shard.
func TestPrincipalHrefFrom(t *testing.T) {
	if got := principalHrefFrom([]byte(principalBody)); got != "/18038948591/principal/" {
		t.Fatalf("principalHrefFrom = %q", got)
	}
	if got := principalHrefFrom([]byte("not xml")); got != "" {
		t.Fatalf("principalHrefFrom on garbage = %q, want empty", got)
	}
}

func TestHomeSetHrefFrom(t *testing.T) {
	want := "https://p143-caldav.icloud.com/18038948591/calendars/"
	if got := homeSetHrefFrom([]byte(principalBody)); got != want {
		t.Fatalf("homeSetHrefFrom = %q, want %q", got, want)
	}
}

func TestDeleteEventIsIdempotent(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"missing is success", http.StatusNotFound, false},
		{"gone is success", http.StatusGone, false},
		{"no content is success", http.StatusNoContent, false},
		{"server error surfaces", http.StatusInternalServerError, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := &CaldavClient{hc: srv.Client(), endpoint: srv.URL}
			err := c.DeleteEvent(context.Background(), "/cal/x.ics")
			if gotMethod != http.MethodDelete {
				t.Fatalf("method = %q, want DELETE", gotMethod)
			}
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
