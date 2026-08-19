package tagrouter_test

import (
	"net/http"
	"testing"

	"github.com/gorilla/websocket"
)

// tokenVerifier accepts one token.
type tokenVerifier struct{ valid string }

func (v tokenVerifier) VerifyToken(token string) (string, bool) {
	if token != "" && token == v.valid {
		return "dev-1", true
	}
	return "", false
}

func newStrictServer(t *testing.T, strict bool) string {
	t.Helper()
	return newStack(t, stackConfig{
		APISecret:     "shared-secret",
		TokenVerifier: tokenVerifier{valid: "paired-token"},
		RequirePaired: strict,
	}).URL
}

func dialStatus(t *testing.T, url string) int {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		_ = conn.Close()
		return http.StatusSwitchingProtocols
	}
	if resp == nil {
		t.Fatalf("dial failed with no response: %v", err)
	}
	return resp.StatusCode
}

// Under strict mode a paired token gets in and nothing else does — not the
// shared secret, and not the loopback bypass the test dialer would otherwise
// benefit from, since httptest listens on 127.0.0.1.
func TestStrictModeAdmitsOnlyPairedDevices(t *testing.T) {
	url := newStrictServer(t, true)

	if code := dialStatus(t, url+"&secret=paired-token"); code != http.StatusSwitchingProtocols {
		t.Errorf("paired device got %d, want an upgrade", code)
	}
	if code := dialStatus(t, url+"&secret=shared-secret"); code != http.StatusUnauthorized {
		t.Errorf("shared secret got %d, want 401", code)
	}
	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("loopback with no credential got %d, want 401", code)
	}
}

// With strict mode off, the previous behavior is intact.
func TestNonStrictModeUnchanged(t *testing.T) {
	url := newStrictServer(t, false)

	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Errorf("loopback got %d, want an upgrade", code)
	}
	if code := dialStatus(t, url+"&secret=shared-secret"); code != http.StatusSwitchingProtocols {
		t.Errorf("shared secret got %d, want an upgrade", code)
	}
	if code := dialStatus(t, url+"&secret=paired-token"); code != http.StatusSwitchingProtocols {
		t.Errorf("paired token got %d, want an upgrade", code)
	}
}

// The requirement is settable while the agent runs, so it can be tried against
// a real device without a restart.
func TestStrictModeTogglesAtRuntime(t *testing.T) {
	st := newStack(t, stackConfig{
		APISecret:     "shared-secret",
		TokenVerifier: tokenVerifier{valid: "paired-token"},
	})
	url, dev := st.URL, st.Auth

	if dev.RequirePaired() {
		t.Error("strict mode defaulted on")
	}
	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Fatalf("loopback got %d before the toggle, want an upgrade", code)
	}

	dev.SetRequirePaired(true)

	if !dev.RequirePaired() {
		t.Error("RequirePairedDevice did not report the change")
	}
	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("loopback got %d after the toggle, want 401", code)
	}

	dev.SetRequirePaired(false)

	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Errorf("loopback got %d after turning it off, want an upgrade", code)
	}
}
