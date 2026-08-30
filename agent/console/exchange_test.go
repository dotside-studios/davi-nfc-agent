//go:build !nowebui

package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
)

func TestExchangeSecretMintsAValidSession(t *testing.T) {
	auth := NewAuth()

	session, ok := auth.ExchangeSecret("s3cret", "s3cret")
	if !ok {
		t.Fatal("a matching secret was refused")
	}
	if !auth.ValidSession(session) {
		t.Error("the exchanged session was not valid")
	}
}

func TestExchangeSecretRefusesAMismatch(t *testing.T) {
	auth := NewAuth()
	if _, ok := auth.ExchangeSecret("wrong", "s3cret"); ok {
		t.Error("a wrong secret minted a session")
	}
}

// The whole point of the fail-closed rule: an agent running without a secret
// must not hand a control session to anyone who reaches the exchange.
func TestExchangeSecretFailsClosedOnAnEmptySecret(t *testing.T) {
	auth := NewAuth()
	if _, ok := auth.ExchangeSecret("", ""); ok {
		t.Error("an empty provided and current secret minted a session")
	}
	if _, ok := auth.ExchangeSecret("anything", ""); ok {
		t.Error("an empty current secret minted a session")
	}
	if _, ok := auth.ExchangeSecret("", "s3cret"); ok {
		t.Error("an empty provided secret minted a session")
	}
}

// exchangeServer builds a console with the exchange route mounted over a fixed
// secret.
func exchangeServer(t *testing.T, secret string) (http.Handler, *Server) {
	t.Helper()
	s := newServer(serverConfig{
		Host:                newFakeHost(),
		Logs:                logbuf.New(64),
		Name:                "davi-nfc-agent",
		Version:             "test",
		AllowSecretExchange: true,
		Secret:              func() string { return secret },
	})
	return s.Handler(), s
}

// exchangeRequest posts to the exchange from loopback and the agent's own
// origin, carrying the secret in the header the endpoint reads.
func exchangeRequest(secret string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/control/exchange", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"
	r.Header.Set("Origin", "https://localhost:9470")
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	return r
}

func TestExchangeEndpointMintsACookieThatAuthorizes(t *testing.T) {
	handler, _ := exchangeServer(t, "s3cret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exchangeRequest("s3cret"))
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange returned %d, want 200", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == cookieName {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("exchange set no session cookie")
	}
	if !session.HttpOnly {
		t.Error("the session cookie is not HttpOnly")
	}

	// The minted cookie must actually pass the control gate.
	state := httptest.NewRequest(http.MethodGet, "/control/state", nil)
	state.RemoteAddr = "127.0.0.1:5555"
	state.Host = "localhost:9470"
	state.Header.Set("Origin", "https://localhost:9470")
	state.AddCookie(session)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, state)
	if rec.Code != http.StatusOK {
		t.Fatalf("the exchanged cookie did not authorize /control/state: got %d", rec.Code)
	}
}

func TestExchangeEndpointRefusesAWrongSecret(t *testing.T) {
	handler, _ := exchangeServer(t, "s3cret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exchangeRequest("wrong"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a wrong secret returned %d, want 403", rec.Code)
	}
}

// The secret must be presented in the header, never the query string: a URL
// lands in logs and history.
func TestExchangeEndpointIgnoresASecretInTheQuery(t *testing.T) {
	handler, _ := exchangeServer(t, "s3cret")

	r := httptest.NewRequest(http.MethodPost, "/control/exchange?secret=s3cret", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"
	r.Header.Set("Origin", "https://localhost:9470")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a secret in the query was accepted: got %d, want 403", rec.Code)
	}
}

func TestExchangeEndpointRejectsNonLoopbackAndCrossOrigin(t *testing.T) {
	handler, _ := exchangeServer(t, "s3cret")

	offHost := exchangeRequest("s3cret")
	offHost.RemoteAddr = "192.168.1.20:5555"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, offHost)
	if rec.Code != http.StatusForbidden {
		t.Errorf("off-host exchange returned %d, want 403", rec.Code)
	}

	crossOrigin := exchangeRequest("s3cret")
	crossOrigin.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, crossOrigin)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin exchange returned %d, want 403", rec.Code)
	}
}

func TestExchangeEndpointRejectsNonPost(t *testing.T) {
	handler, _ := exchangeServer(t, "s3cret")

	r := httptest.NewRequest(http.MethodGet, "/control/exchange", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Host = "localhost:9470"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on the exchange returned %d, want 405", rec.Code)
	}
}

// With an empty configured secret, the endpoint refuses even a request that
// presents an empty secret — fail closed, end to end.
func TestExchangeEndpointFailsClosedWithNoConfiguredSecret(t *testing.T) {
	handler, _ := exchangeServer(t, "")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exchangeRequest("anything"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("exchange with no configured secret returned %d, want 403", rec.Code)
	}
}

// The route is absent unless the build opts in.
func TestExchangeEndpointIsNotMountedByDefault(t *testing.T) {
	s := newServer(serverConfig{Host: newFakeHost(), Name: "davi-nfc-agent", Version: "test"})
	handler := s.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exchangeRequest("s3cret"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("exchange route answered %d without opt-in, want 404", rec.Code)
	}
}
