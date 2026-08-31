//go:build !nowebui

package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
)

func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{
		"":               DefaultBasePath,
		"/control/":      "/control/",
		"control":        "/control/",
		"/agent-console": "/agent-console/",
		"agent-console/": "/agent-console/",
		"/nested/panel/": "/nested/panel/",
	}
	for in, want := range cases {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// baseServer builds a console at a custom base with the exchange enabled, plus a
// session cookie that authorises a caller.
func baseServer(t *testing.T, base string) (http.Handler, *Server, *http.Cookie) {
	t.Helper()
	host := newFakeHost()
	host.port = 9470
	s := newServer(serverConfig{
		Host:                host,
		Logs:                logbuf.New(64),
		Name:                "davi-nfc-agent",
		Version:             "test",
		BasePath:            base,
		AllowSecretExchange: true,
		Secret:              func() string { return "s3cret" },
	})
	token, _ := s.auth.MintHandoff()
	session, ok := s.auth.RedeemHandoff(token)
	if !ok {
		t.Fatal("could not mint a test session")
	}
	return s.Handler(), s, &http.Cookie{Name: cookieName, Value: session}
}

func loopbackGet(path string, cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"
	r.Header.Set("Origin", "https://localhost:9470")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestControlRoutesMoveWithACustomBasePath(t *testing.T) {
	handler, _, cookie := baseServer(t, "/agent-console/")

	// The API answers under the custom base.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, loopbackGet("/agent-console/state", cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agent-console/state = %d, want 200", rec.Code)
	}

	// And nothing is left at the default base.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, loopbackGet("/control/state", cookie))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /control/state = %d under a custom base, want 404", rec.Code)
	}
}

func TestExchangeRouteMovesWithTheBasePath(t *testing.T) {
	handler, _, _ := baseServer(t, "/agent-console/")

	post := func(path string) int {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.RemoteAddr = "127.0.0.1:5555"
		r.Host = "localhost:9470"
		r.Header.Set("Origin", "https://localhost:9470")
		r.Header.Set("Authorization", "Bearer s3cret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := post("/agent-console/exchange"); code != http.StatusOK {
		t.Errorf("POST /agent-console/exchange = %d, want 200", code)
	}
	if code := post("/control/exchange"); code != http.StatusNotFound {
		t.Errorf("POST /control/exchange = %d under a custom base, want 404", code)
	}
}

func TestConsoleURLUsesTheBasePath(t *testing.T) {
	_, s, _ := baseServer(t, "/agent-console/")
	url, err := s.ConsoleURL()
	if err != nil {
		t.Fatalf("ConsoleURL: %v", err)
	}
	if !strings.Contains(url, "/agent-console/session?token=") {
		t.Errorf("ConsoleURL = %q, want it to carry the custom base's session path", url)
	}
}

func TestEndpointsServeThePageOnlyAtTheDefaultBase(t *testing.T) {
	a := quietAgent(t)

	// Default base: the API and the bundled page, the page at the root.
	def := New(Config{Agent: a}).Endpoints()
	if len(def) != 2 {
		t.Fatalf("default Endpoints() has %d entries, want 2 (API + page)", len(def))
	}
	if def[0].Pattern != DefaultBasePath {
		t.Errorf("default API mounts at %q, want %q", def[0].Pattern, DefaultBasePath)
	}
	if def[1].Pattern != "/" {
		t.Errorf("the page mounts at %q, want \"/\"", def[1].Pattern)
	}

	// Custom base: the API alone, under the custom base, and nothing at the root.
	custom := New(Config{Agent: a, BasePath: "/agent-console/"}).Endpoints()
	if len(custom) != 1 {
		t.Fatalf("custom-base Endpoints() has %d entries, want 1 (API only)", len(custom))
	}
	if custom[0].Pattern != "/agent-console/" {
		t.Errorf("custom API mounts at %q, want \"/agent-console/\"", custom[0].Pattern)
	}
}
