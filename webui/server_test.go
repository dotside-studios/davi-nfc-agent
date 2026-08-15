package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// contains reports membership, for asserting on a slice the host returns.
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// newTestServer returns a console over a fake host, plus a session cookie for
// an authorised caller.
func newTestServer(t *testing.T) (*Server, *fakeHost, http.Handler, *http.Cookie) {
	t.Helper()

	host := newFakeHost()
	console := New(Config{Host: host, Logs: logbuf.New(64), Name: "davi-nfc-agent", Version: "test"})

	token, _ := console.auth.MintHandoff()
	session, ok := console.auth.RedeemHandoff(token)
	if !ok {
		t.Fatal("could not mint a test session")
	}

	return console, host, console.Handler(), &http.Cookie{Name: cookieName, Value: session}
}

// authorized builds a request that passes all three gates.
func authorized(method, path string, body string, cookie *http.Cookie) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"
	r.Header.Set("Origin", "https://localhost:9470")
	r.AddCookie(cookie)
	return r
}

func TestControlRoutesRequireASession(t *testing.T) {
	_, _, handler, _ := newTestServer(t)

	for _, path := range []string{"/control/state", "/control/logs", "/control/action", "/control/ws"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "127.0.0.1:5555"
		r.Host = "localhost:9470"

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s without a session: status %d, want 403", path, w.Code)
		}
	}
}

// The origin allowlist authorises a console to read tags. It must never be what
// decides who can revoke devices or rotate the secret.
func TestControlIgnoresTheOriginAllowlist(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)

	if err := host.AllowOrigin("console.example.com"); err != nil {
		t.Fatalf("AllowOrigin: %v", err)
	}

	r := authorized(http.MethodGet, "/control/state", "", cookie)
	r.Header.Set("Origin", "https://console.example.com")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("allowlisted origin reached the control API: status %d, want 403", w.Code)
	}
}

func TestStateSnapshot(t *testing.T) {
	_, _, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodGet, "/control/state", "", cookie))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var state State
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if state.Agent.Name == "" {
		t.Error("agent name missing")
	}
	if state.Server.Port != 9470 {
		t.Errorf("port = %d, want 9470", state.Server.Port)
	}
	if state.Security.APISecret != "test-secret" {
		t.Errorf("apiSecret = %q", state.Security.APISecret)
	}
	if len(state.Reader.AllCardTypes) == 0 {
		t.Error("allCardTypes empty; the settings panel has nothing to offer")
	}
	// Slices must marshal as [] rather than null so the console can map over
	// them without guarding every one.
	for _, key := range []string{`"devices":[]`, `"clients":[]`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Errorf("empty slice marshalled as null, not []: missing %s", key)
		}
	}
}

func TestClientsDisconnectRejectsUnknownID(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)
	host.failDisconnect = true

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"clients.disconnect","params":{"id":"nope"}}`, cookie))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestActionRejectsUnknownName(t *testing.T) {
	_, _, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action", `{"action":"rm -rf"}`, cookie))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown action") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestActionRequiresPost(t *testing.T) {
	_, _, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodGet, "/control/action", "", cookie))

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", w.Code)
	}
}

func TestSettingsActionPersistsAndApplies(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"reader.setMode","params":{"mode":"read"}}`, cookie))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := host.Settings().Mode; got != settings.ModeReadOnly {
		t.Errorf("stored mode = %q, want %q", got, settings.ModeReadOnly)
	}

	// And it comes back in the next snapshot.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodGet, "/control/state", "", cookie))

	var state State
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.Settings.Mode != settings.ModeReadOnly {
		t.Errorf("snapshot mode = %q", state.Settings.Mode)
	}
}

func TestSetModeRejectsUnknownMode(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"reader.setMode","params":{"mode":"sideways"}}`, cookie))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	if got := host.Settings().Mode; got != settings.ModeReadWrite {
		t.Errorf("mode changed to %q despite the error", got)
	}
}

func TestOriginActionsRoundTrip(t *testing.T) {
	_, host, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"origins.allow","params":{"origin":"app.example.com"}}`, cookie))
	if w.Code != http.StatusOK {
		t.Fatalf("allow: status %d: %s", w.Code, w.Body.String())
	}
	if !contains(host.AllowedOrigins(), "app.example.com") {
		t.Error("origin not allowed after origins.allow")
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/action",
		`{"action":"origins.revoke","params":{"origin":"app.example.com"}}`, cookie))
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: status %d: %s", w.Code, w.Body.String())
	}
	if contains(host.AllowedOrigins(), "app.example.com") {
		t.Error("origin still allowed after origins.revoke")
	}
}

func TestLogsEndpointHonoursSince(t *testing.T) {
	console, _, handler, cookie := newTestServer(t)

	_, _ = console.logs.Write([]byte("first\nsecond\nthird\n"))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodGet, "/control/logs?since=2", "", cookie))

	var entries []logbuf.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0].Message != "third" {
		t.Errorf("entries = %+v, want just \"third\"", entries)
	}
}

func TestSignoutEndsTheSession(t *testing.T) {
	console, _, handler, cookie := newTestServer(t)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodPost, "/control/signout", "", cookie))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}

	if console.auth.ValidSession(cookie.Value) {
		t.Error("session still valid after signout")
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, authorized(http.MethodGet, "/control/state", "", cookie))
	if w.Code != http.StatusForbidden {
		t.Errorf("state after signout: status %d, want 403", w.Code)
	}
}

func TestSessionHandoffSetsCookieAndRedirects(t *testing.T) {
	console, _, handler, _ := newTestServer(t)

	token, err := console.auth.MintHandoff()
	if err != nil {
		t.Fatalf("MintHandoff: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/control/session?token="+token, nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", w.Code)
	}
	// The spent token must not remain in the address bar or the history.
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}

	var session string
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			session = c.Value
			if !c.HttpOnly {
				t.Error("session cookie is not HttpOnly")
			}
		}
	}
	if session == "" {
		t.Fatal("no session cookie set")
	}
	if !console.auth.ValidSession(session) {
		t.Error("issued session is not valid")
	}
}

func TestSessionHandoffRejectsSpentToken(t *testing.T) {
	console, _, handler, _ := newTestServer(t)

	token, _ := console.auth.MintHandoff()
	console.auth.RedeemHandoff(token)

	r := httptest.NewRequest(http.MethodGet, "/control/session?token="+token, nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
}

func TestSessionHandoffRejectsOffHost(t *testing.T) {
	console, _, handler, _ := newTestServer(t)

	token, _ := console.auth.MintHandoff()

	r := httptest.NewRequest(http.MethodGet, "/control/session?token="+token, nil)
	r.RemoteAddr = "192.168.1.44:5555"
	r.Host = "192.168.1.10:9470"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
	// The token must survive an unauthorised attempt, or anyone able to reach
	// the port could burn tokens as fast as the tray mints them.
	if _, ok := console.auth.RedeemHandoff(token); !ok {
		t.Error("a refused request consumed the handoff token")
	}
}

func TestConsoleURLIsLoopback(t *testing.T) {
	console, _, _, _ := newTestServer(t)

	url, err := console.ConsoleURL()
	if err != nil {
		t.Fatalf("ConsoleURL: %v", err)
	}
	if !strings.HasPrefix(url, "http://localhost:9470/control/session?token=") {
		t.Errorf("ConsoleURL = %q", url)
	}
}
