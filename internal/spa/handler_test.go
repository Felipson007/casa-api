package spa

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestNewHandler_ServesExactFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_next/static/app.js", "console.log('hi')")

	h := NewHandler(dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_next/static/app.js", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "console.log('hi')" {
		t.Fatalf("expected the real asset file back, got status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestNewHandler_FallsBackToRouteIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dashboard/index.html", "<html>dashboard</html>")

	h := NewHandler(dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>dashboard</html>" {
		t.Fatalf("expected dashboard/index.html, got status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestNewHandler_FallsBackToRootIndexForUnknownClientRoute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<html>root shell</html>")

	h := NewHandler(dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/client/side/route", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>root shell</html>" {
		t.Fatalf("expected the root shell as a last resort, got status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestNewHandler_MissingDirNotFoundInsteadOfPanic(t *testing.T) {
	h := NewHandler(filepath.Join(t.TempDir(), "does-not-exist"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when the static dir doesn't exist, got %d", rec.Code)
	}
}
