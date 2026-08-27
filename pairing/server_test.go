package pairing_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/pairing"
)

// pairOver posts a pairing request to the handler, over TLS as the endpoint
// requires, and returns the decoded response.
func pairOver(t *testing.T, h http.Handler, pin string) map[string]any {
	t.Helper()

	ts := httptest.NewTLSServer(h)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"?pin="+pin, "application/json",
		strings.NewReader(`{"deviceName":"phone","platform":"android"}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pairing failed with status %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// The port a pairing device is told to connect to is read when it pairs, not
// captured when the endpoint was built.
//
// It used to be captured. The port is a saved preference the operator can
// change from the control center, so a device pairing after such a change was
// handed the old port and could not connect.
func TestThePairingResponseCarriesTheCurrentPort(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	port := 9000
	server := pairing.NewServer(pairing.ServerOptions{
		Registry:     registry,
		PublicKeyPin: func() string { return "sha256/test" },
		AgentPort:    func() int { return port },
	})

	first := pairOver(t, server.PairHandler(), server.PIN())
	if got := first["agentPort"]; got != float64(9000) {
		t.Fatalf("agentPort = %v, want 9000", got)
	}

	// The operator moves the agent to another port while this endpoint stays up.
	port = 9500

	second := pairOver(t, server.PairHandler(), server.PIN())
	if got := second["agentPort"]; got != float64(9500) {
		t.Errorf("agentPort = %v, want the port the agent is on now (9500)", got)
	}
}

// The key pin follows the certificate, which can be reissued while the endpoint
// stays up, so it is read per pairing too.
func TestThePairingResponseCarriesTheCurrentKeyPin(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	pin := "sha256/first"
	server := pairing.NewServer(pairing.ServerOptions{
		Registry:     registry,
		PublicKeyPin: func() string { return pin },
		AgentPort:    func() int { return 9000 },
	})

	if got := pairOver(t, server.PairHandler(), server.PIN())["publicKeyPin"]; got != "sha256/first" {
		t.Fatalf("publicKeyPin = %v, want the current pin", got)
	}

	pin = "sha256/reissued"

	if got := pairOver(t, server.PairHandler(), server.PIN())["publicKeyPin"]; got != "sha256/reissued" {
		t.Errorf("publicKeyPin = %v, want the reissued pin", got)
	}
}

// Each pairing issues its own credential, and the token is what the device
// presents afterwards.
func TestPairingIssuesADistinctCredentialEachTime(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	server := pairing.NewServer(pairing.ServerOptions{
		Registry:     registry,
		PublicKeyPin: func() string { return "sha256/test" },
		AgentPort:    func() int { return 9000 },
	})

	first := pairOver(t, server.PairHandler(), server.PIN())
	second := pairOver(t, server.PairHandler(), server.PIN())

	if first["deviceToken"] == second["deviceToken"] {
		t.Error("two pairings issued the same credential")
	}
	if first["deviceID"] == second["deviceID"] {
		t.Error("two pairings issued the same identity")
	}
	if got := registry.Count(); got != 2 {
		t.Errorf("Count() = %d, want both paired devices", got)
	}

	token, _ := second["deviceToken"].(string)
	if id, ok := registry.VerifyToken(token); !ok || id != second["deviceID"] {
		t.Errorf("VerifyToken on the issued credential = (%q, %v), want the device it named", id, ok)
	}
}

// A build handing out a certificate authority and nothing else serves the pages
// but pairs nobody, rather than issuing into a store it has not got.
func TestAServerWithNoRegistryPairsNobody(t *testing.T) {
	server := pairing.NewServer(pairing.ServerOptions{})

	ts := httptest.NewTLSServer(server.PairHandler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"?pin="+server.PIN(), "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotImplemented)
	}
}

// pairInTheClear posts a pairing request with no TLS, from remoteAddr.
func pairInTheClear(h http.Handler, pin, remoteAddr string) int {
	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+pin, strings.NewReader(`{}`))
	r.RemoteAddr = remoteAddr
	r.TLS = nil

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// The credential and the key pin would be readable in the clear, and the pin
// substitutable by anyone in the path, so cleartext pairing from off this
// machine is refused.
func TestPairingRefusesCleartextFromOffThisMachine(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	server := pairing.NewServer(pairing.ServerOptions{
		Registry:     registry,
		PublicKeyPin: func() string { return "sha256/test" },
		AgentPort:    func() int { return 9000 },
	})

	if got := pairInTheClear(server.PairHandler(), server.PIN(), "192.0.2.7:34512"); got != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want %d", got, http.StatusUpgradeRequired)
	}
	if registry.Count() != 0 {
		t.Error("a credential was issued over a cleartext connection")
	}
}

// From this machine there is no path to observe or substitute on, so cleartext
// is allowed, as with the loopback bypass on the device endpoint.
func TestPairingAllowsCleartextFromThisMachine(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	server := pairing.NewServer(pairing.ServerOptions{
		Registry:     registry,
		PublicKeyPin: func() string { return "sha256/test" },
		AgentPort:    func() int { return 9000 },
	})

	if got := pairInTheClear(server.PairHandler(), server.PIN(), "127.0.0.1:34512"); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if registry.Count() != 1 {
		t.Error("pairing from this machine issued no credential")
	}
}
