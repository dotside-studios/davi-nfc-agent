//go:build !nocontrol

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIIsEmbedded(t *testing.T) {
	if webUIHandler() == nil {
		t.Fatal("no console embedded; run `make webui` and commit webui/dist")
	}
}

func TestServesTheAppShell(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<div id=\"root\">") {
		t.Errorf("body does not look like the app shell: %.200s", body)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
	}
	// The shell names fingerprinted assets, so a cached copy would survive an
	// agent upgrade and keep loading the previous build.
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", w.Header().Get("Cache-Control"))
	}
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), want) {
			t.Errorf("CSP missing %q: %s", want, w.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestUnknownPathFallsBackToTheShell(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/devices", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200 so the app can resolve the route", w.Code)
	}
}

// HTML for a missing .js only produces an unexpected-"<" error that hides the
// real problem.
func TestMissingAssetIs404(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	for _, path := range []string{"/assets/gone.js", "/assets/gone.css", "/missing.png"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
	}
}

// The control API is mounted separately; a stray request must not be answered
// with the shell and a 200.
func TestControlPathsNeverReachTheFileServer(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/control/state", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestFingerprintedAssetsAreCacheable(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	// Discover a real asset name from the shell rather than hard-coding a hash.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	body := w.Body.String()
	idx := strings.Index(body, "assets/")
	if idx < 0 {
		t.Skip("shell references no fingerprinted assets")
	}
	end := strings.IndexAny(body[idx:], "\"'")
	if end < 0 {
		t.Fatal("could not parse an asset name out of the shell")
	}
	asset := "/" + body[idx:idx+end]

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, asset, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d", asset, w.Code)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("%s: Cache-Control = %q", asset, w.Header().Get("Cache-Control"))
	}
}

// Traversal must not escape the embedded filesystem.
func TestPathTraversalIsRefused(t *testing.T) {
	h := webUIHandler()
	if h == nil {
		t.Skip("console not built")
	}

	for _, path := range []string{"/../agent.go", "/assets/../../go.mod", "/../../etc/passwd"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		if strings.Contains(w.Body.String(), "package main") || strings.Contains(w.Body.String(), "root:") {
			t.Errorf("%s leaked file contents", path)
		}
	}
}
