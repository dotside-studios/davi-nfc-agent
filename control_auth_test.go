package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandoffIsSingleUse(t *testing.T) {
	auth := NewControlAuth()

	token, err := auth.MintHandoff()
	if err != nil {
		t.Fatalf("MintHandoff: %v", err)
	}

	session, ok := auth.RedeemHandoff(token)
	if !ok {
		t.Fatal("first redemption failed")
	}
	if session == "" {
		t.Fatal("redemption returned an empty session token")
	}

	// A URL that leaks into shell history or browser autocomplete must already
	// be spent by the time anyone finds it.
	if _, ok := auth.RedeemHandoff(token); ok {
		t.Error("handoff token redeemed twice")
	}
}

func TestRedeemRejectsUnknownToken(t *testing.T) {
	auth := NewControlAuth()
	if _, ok := auth.RedeemHandoff("not-a-real-token"); ok {
		t.Error("unknown token was redeemed")
	}
	if _, ok := auth.RedeemHandoff(""); ok {
		t.Error("empty token was redeemed")
	}
}

func TestSessionValidity(t *testing.T) {
	auth := NewControlAuth()

	token, _ := auth.MintHandoff()
	session, _ := auth.RedeemHandoff(token)

	if !auth.ValidSession(session) {
		t.Error("freshly issued session rejected")
	}
	if auth.ValidSession("some-other-token") {
		t.Error("unknown session accepted")
	}
	if auth.ValidSession("") {
		t.Error("empty session accepted")
	}

	auth.RevokeSession(session)
	if auth.ValidSession(session) {
		t.Error("revoked session still valid")
	}
}

func TestRevokeAllClearsSessionsAndHandoffs(t *testing.T) {
	auth := NewControlAuth()

	unclaimed, _ := auth.MintHandoff()
	claimed, _ := auth.MintHandoff()
	session, _ := auth.RedeemHandoff(claimed)

	auth.RevokeAll()

	if auth.ValidSession(session) {
		t.Error("session survived RevokeAll")
	}
	if _, ok := auth.RedeemHandoff(unclaimed); ok {
		t.Error("unclaimed handoff survived RevokeAll")
	}
	if got := auth.SessionCount(); got != 0 {
		t.Errorf("SessionCount = %d, want 0", got)
	}
}

func TestExpiredTokensAreRejected(t *testing.T) {
	auth := NewControlAuth()

	token, _ := auth.MintHandoff()
	session, _ := auth.RedeemHandoff(token)

	// Reach past the clock rather than sleeping for the real TTL.
	auth.mu.Lock()
	for k := range auth.sessions {
		auth.sessions[k] = auth.sessions[k].Add(-controlSessionTTL - time.Hour)
	}
	auth.mu.Unlock()

	if auth.ValidSession(session) {
		t.Error("expired session accepted")
	}
}

func TestExpiredHandoffIsRejected(t *testing.T) {
	auth := NewControlAuth()
	token, _ := auth.MintHandoff()

	auth.mu.Lock()
	for k := range auth.handoff {
		auth.handoff[k] = auth.handoff[k].Add(-controlTokenTTL - time.Hour)
	}
	auth.mu.Unlock()

	if _, ok := auth.RedeemHandoff(token); ok {
		t.Error("expired handoff token redeemed")
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		remoteAddr string
		want       bool
	}{
		{"127.0.0.1:54321", true},
		{"[::1]:54321", true},
		{"127.0.0.53:9", true},
		{"192.168.1.50:54321", false},
		{"10.0.0.1:80", false},
		{"", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/control/state", nil)
		r.RemoteAddr = tc.remoteAddr
		if got := isLoopbackRequest(r); got != tc.want {
			t.Errorf("isLoopbackRequest(%q) = %v, want %v", tc.remoteAddr, got, tc.want)
		}
	}
}

// A forwarding header is attacker-controlled. Treating one as evidence of
// locality is how a control surface ends up reachable from off the machine.
func TestForwardedHeaderCannotForgeLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/control/state", nil)
	r.RemoteAddr = "203.0.113.7:44444"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")

	if isLoopbackRequest(r) {
		t.Error("a spoofed forwarding header was treated as loopback")
	}
}

func TestIsSameOriginRequest(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		refer  string
		want   bool
	}{
		{"no origin or referer (direct navigation)", "localhost:9470", "", "", true},
		{"matching origin", "localhost:9470", "https://localhost:9470", "", true},
		{"matching referer", "localhost:9470", "", "https://localhost:9470/console", true},
		{"different port", "localhost:9470", "https://localhost:3000", "", false},
		{"different host", "localhost:9470", "https://evil.example.com", "", false},
		{"loopback ip form", "127.0.0.1:9470", "https://127.0.0.1:9470", "", true},
		{"case-insensitive host", "LocalHost:9470", "https://localhost:9470", "", true},
		{"unparseable origin", "localhost:9470", "://///", "", false},
		{"null origin", "localhost:9470", "null", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/control/state", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.refer != "" {
				r.Header.Set("Referer", tc.refer)
			}
			if got := isSameOriginRequest(r); got != tc.want {
				t.Errorf("isSameOriginRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAuthorizeControlRequest(t *testing.T) {
	auth := NewControlAuth()
	token, _ := auth.MintHandoff()
	session, _ := auth.RedeemHandoff(token)

	newRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/control/state", nil)
		r.RemoteAddr = "127.0.0.1:5555"
		r.Host = "localhost:9470"
		r.Header.Set("Origin", "https://localhost:9470")
		r.AddCookie(&http.Cookie{Name: controlCookieName, Value: session})
		return r
	}

	if reason := auth.authorizeControlRequest(newRequest()); reason != "" {
		t.Fatalf("valid request refused: %s", reason)
	}

	remote := newRequest()
	remote.RemoteAddr = "192.168.1.20:5555"
	if reason := auth.authorizeControlRequest(remote); reason != "not loopback" {
		t.Errorf("off-host request: got %q, want %q", reason, "not loopback")
	}

	// The browser attaches the session cookie to a cross-site request too, so
	// loopback plus a valid cookie is not sufficient on its own.
	crossSite := newRequest()
	crossSite.Header.Set("Origin", "https://evil.example.com")
	if reason := auth.authorizeControlRequest(crossSite); reason != "cross-origin" {
		t.Errorf("cross-site request: got %q, want %q", reason, "cross-origin")
	}

	noCookie := httptest.NewRequest(http.MethodGet, "/control/state", nil)
	noCookie.RemoteAddr = "127.0.0.1:5555"
	noCookie.Host = "localhost:9470"
	if reason := auth.authorizeControlRequest(noCookie); reason != "no valid session" {
		t.Errorf("cookieless request: got %q, want %q", reason, "no valid session")
	}

	stale := newRequest()
	auth.RevokeSession(session)
	if reason := auth.authorizeControlRequest(stale); reason != "no valid session" {
		t.Errorf("revoked session: got %q, want %q", reason, "no valid session")
	}
}
