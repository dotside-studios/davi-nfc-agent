package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The console's built assets are compiled into the binary. frontend/dist is
// committed so `go build .` works without Node; rebuild it with `make webui`.
//
//go:embed all:frontend/dist
var frontendFS embed.FS

// Console serves the built console, or nil if no build is embedded.
func Console() http.Handler {
	dist, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return nil
	}
	return &spaHandler{root: dist, files: http.FileServer(http.FS(dist))}
}

// spaHandler serves the built console, falling back to index.html for paths
// that do not name a file.
type spaHandler struct {
	root  fs.FS
	files http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Mounted separately; a stray request here must not get the shell and a 200.
	if strings.HasPrefix(r.URL.Path, "/control/") {
		http.NotFound(w, r)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		h.serveIndex(w, r)
		return
	}

	if _, err := fs.Stat(h.root, name); err != nil {
		// A missing asset is a 404, not the shell: HTML for a missing .js only
		// produces an unexpected-"<" error that hides the real problem.
		if ext := path.Ext(name); ext != "" && ext != ".html" {
			http.NotFound(w, r)
			return
		}
		h.serveIndex(w, r)
		return
	}

	// Vite fingerprints these names, so they can be cached indefinitely.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	h.files.ServeHTTP(w, r)
}

// serveIndex writes the app shell. Never cached: it names the fingerprinted
// assets, so a stale copy would survive an upgrade.
func (h *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(h.root, "index.html")
	if err != nil {
		http.Error(w, "control center not built", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	// The console is self-contained: no third-party code, no framing.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; "+
			"connect-src 'self' ws: wss:; "+
			"font-src 'self'; "+
			"object-src 'none'; "+
			"base-uri 'none'; "+
			"form-action 'none'; "+
			"frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}
