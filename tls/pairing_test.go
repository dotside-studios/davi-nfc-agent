package tls

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubIssuer stands in for the device registry.
type stubIssuer struct {
	calls int
	name  string
	plat  string
}

func (s *stubIssuer) Pair(name, platform string) (string, string, error) {
	s.calls++
	s.name, s.plat = name, platform
	return "dev-1", "issued-token", nil
}

func (s *stubIssuer) PublicKeyPin() string { return "sha256/testpin" }

func newPairingServer(t *testing.T) (*BootstrapServer, *stubIssuer) {
	t.Helper()

	s := NewBootstrapServer(newFakeCAReader(t), 0)
	issuer := &stubIssuer{}
	s.SetPairingIssuer(issuer, 9470)
	return s, issuer
}

func TestPairIssuesCredential(t *testing.T) {
	s, issuer := newPairingServer(t)

	body := strings.NewReader(`{"deviceName":"Operator iPhone","platform":"ios"}`)
	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), body)
	w := httptest.NewRecorder()

	s.handlePair(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var resp PairingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if resp.DeviceToken != "issued-token" {
		t.Errorf("DeviceToken = %q, want issued-token", resp.DeviceToken)
	}
	// The device learns how to recognize the agent later, in the same exchange.
	if resp.PublicKeyPin != "sha256/testpin" {
		t.Errorf("PublicKeyPin = %q, want sha256/testpin", resp.PublicKeyPin)
	}
	if resp.AgentPort != 9470 {
		t.Errorf("AgentPort = %d, want 9470", resp.AgentPort)
	}
	if issuer.name != "Operator iPhone" || issuer.plat != "ios" {
		t.Errorf("issuer got name=%q platform=%q", issuer.name, issuer.plat)
	}
}

// The PIN is what proves the caller can see the kiosk. Without it, pairing
// would be open to anyone who can reach the port.
func TestPairRequiresPIN(t *testing.T) {
	s, issuer := newPairingServer(t)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin=000000", nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if issuer.calls != 0 {
		t.Error("a credential was issued without the PIN")
	}
}

func TestPairRejectsGet(t *testing.T) {
	s, issuer := newPairingServer(t)

	r := httptest.NewRequest(http.MethodGet, "/pair?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if issuer.calls != 0 {
		t.Error("a GET issued a credential")
	}
}

// An agent with no registry says so, rather than 404, so a device can tell
// "cannot pair" from "wrong address".
func TestPairWithoutIssuer(t *testing.T) {
	s := NewBootstrapServer(newFakeCAReader(t), 0)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}

// A device that sends no body still pairs; it just shows up unnamed.
func TestPairWithoutBody(t *testing.T) {
	s, _ := newPairingServer(t)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// Repeated wrong PINs lock pairing, as they lock the CA download.
func TestPairLocksAfterRepeatedWrongPIN(t *testing.T) {
	s, issuer := newPairingServer(t)

	for i := 0; i < bootstrapMaxFailures; i++ {
		r := httptest.NewRequest(http.MethodPost, "/pair?pin=000000", nil)
		s.handlePair(httptest.NewRecorder(), r)
	}

	// Even the correct PIN is refused once locked.
	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w.Code)
	}
	if issuer.calls != 0 {
		t.Error("a credential was issued after lockout")
	}
}

// An agent using an externally provisioned certificate has no CA to hand out,
// but must still be able to pair devices, or the deployment that avoids
// installing a CA has no way to authenticate one.
func TestPairWorksWithoutCA(t *testing.T) {
	s := NewBootstrapServer(nil, 0)
	issuer := &stubIssuer{}
	s.SetPairingIssuer(issuer, 9470)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if issuer.calls != 1 {
		t.Errorf("issuer calls = %d, want 1", issuer.calls)
	}
}

// With no CA, the install endpoints say so rather than crashing.
func TestCAEndpointsWithoutCA(t *testing.T) {
	s := NewBootstrapServer(nil, 0)

	r := httptest.NewRequest(http.MethodGet, "/ca.pem?pin="+s.PIN(), nil)
	w := httptest.NewRecorder()
	s.handleRawCA(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}
