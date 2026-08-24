package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallCAAction(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)

	if host.CAInstalled() {
		t.Fatal("fixture starts with a CA already installed")
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"security.installCA"}`, cookie))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if host.installedCA != 1 {
		t.Errorf("InstallCA called %d times, want 1", host.installedCA)
	}
	if !host.CAInstalled() {
		t.Error("CA still reports as not installed")
	}
	// A browser only sees the reissued certificate on a fresh listener.
	if host.rebound != 1 {
		t.Errorf("listener rebound %d times, want 1", host.rebound)
	}
	// The certificate is all that changed, so the connections' backing state
	// is not rebuilt with it.
	if host.restarted != 0 {
		t.Errorf("servers restarted %d times, want none for a certificate", host.restarted)
	}
}

// The install prompts for a password and the operator can refuse it. That has
// to reach the console as an error, not a silent success.
func TestInstallCAReportsFailure(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)
	host.failInstallCA = true

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"security.installCA"}`, cookie))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "trust store") {
		t.Errorf("body does not carry the reason: %s", w.Body.String())
	}
	if host.restarted != 0 {
		t.Error("listeners restarted despite the install failing")
	}
	if host.CAInstalled() {
		t.Error("a failed install reported the CA as installed")
	}
}
