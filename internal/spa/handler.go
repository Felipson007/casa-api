// Package spa serves a Next.js static export (NEXT_OUTPUT_EXPORT=1) with client-side-route
// fallback — the Go equivalent of the Java app's SpaWebConfig/SpaFallbackResourceResolver
// (src/backend/src/main/java/br/com/atlas/config/SpaWebConfig.java), read from that source rather
// than guessed. Mounted on the root mux's "/" (see cmd/api/main.go) — never wrapped in
// httpx.AuthMiddleware, since the SPA shell itself must load before a user is authenticated;
// "/api/" is registered as the more specific pattern that actually needs a session.
package spa

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// indexPage is served directly via serveFile, never through http.FileServer — FileServer has a
// built-in quirk (net/http's serveFile) that 301-redirects any request whose path ends in
// "index.html" to its parent directory (to avoid duplicate-content URLs for a real browser
// request). That's the right behavior for a literal external request to ".../index.html", but
// wrong here: fallback cases below construct that path internally to resolve what a client-side
// route should render, and a redirect back to a path that doesn't re-resolve the same way would
// loop or 404 instead of actually serving the shell.
const indexPage = "index.html"

// NewHandler serves dir as a Next.js static export. Each exported route is a directory with its
// own index.html (e.g. "dashboard" → "dashboard/index.html") — no dynamic segments exist in this
// app on purpose (see the Java SpaWebConfig doc comment) — so a direct navigation to /dashboard
// needs this fallback chain, not just a plain file server:
//  1. the exact requested path, if it's a real file (JS/CSS/image assets)
//  2. "{path}/index.html" (a Next.js route)
//  3. "/index.html" (client-side routes the exporter didn't know about, or the SPA root)
//
// If dir doesn't exist at all (e.g. this binary was built without the frontend-build Dockerfile
// stage — a backend-only deploy, mirroring Java's Dockerfile.render path), every request falls
// through to 404 rather than panicking.
func NewHandler(dir string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fileExists(dir, r.URL.Path) {
			fileServer.ServeHTTP(w, r)
			return
		}

		trimmed := strings.TrimSuffix(r.URL.Path, "/")
		if fileExists(dir, trimmed+"/"+indexPage) {
			serveFile(w, r, filepath.Join(dir, trimmed, indexPage))
			return
		}

		if fileExists(dir, "/"+indexPage) {
			serveFile(w, r, filepath.Join(dir, indexPage))
			return
		}

		http.NotFound(w, r)
	})
}

func fileExists(dir, requestPath string) bool {
	p := filepath.Join(dir, filepath.Clean("/"+requestPath))
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// serveFile opens and streams path directly via http.ServeContent — bypassing http.FileServer
// (and its index.html-redirect quirk, see indexPage's doc comment) entirely for the fallback cases.
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path, info.ModTime(), f)
}
