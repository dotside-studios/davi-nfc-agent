package tls

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The pairing response carries a durable token and the pin the device will
// recognise the agent by afterwards. Issuing it in the clear hands both to an
// observer, and lets an active attacker substitute a pin of their own, which is
// the one value whose purpose is to prevent that.
func TestPairRefusedOverCleartext(t *testing.T) {
	s, issuer := newPairingServer(t)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	r.RemoteAddr = "192.0.2.9:1234"
	w := httptest.NewRecorder()

	s.handlePair(w, r)

	if w.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUpgradeRequired)
	}
	if issuer.calls != 0 {
		t.Error("a credential was issued over a cleartext connection")
	}
}

// Loopback has no network to observe, and an agent with no certificate can
// still pair its own machine.
func TestPairAllowedOverLoopback(t *testing.T) {
	s, issuer := newPairingServer(t)

	r := httptest.NewRequest(http.MethodPost, "/pair?pin="+s.PIN(), nil)
	r.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()

	s.handlePair(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if issuer.calls != 1 {
		t.Errorf("issuer called %d times, want 1", issuer.calls)
	}
}

// The URI carries the pin, so a device can authenticate the pairing connection
// before trusting anything it says, and names the agent's port, which is where
// pairing is served.
func TestPairingURICarriesTheKeyPin(t *testing.T) {
	s, _ := newPairingServer(t)

	uri, err := s.PairingURI("192.0.2.7")
	if err != nil {
		t.Fatalf("PairingURI: %v", err)
	}
	if uri.SPKI != "sha256/testpin" {
		t.Errorf("SPKI = %q, want the agent's key pin", uri.SPKI)
	}
	if uri.Code != s.PIN() {
		t.Errorf("Code = %q, want the current PIN %q", uri.Code, s.PIN())
	}
	if uri.Port != 9470 {
		t.Errorf("Port = %d, want the agent's port 9470, not the bootstrap port", uri.Port)
	}
}

// Without a key pin there is nothing for the device to authenticate pairing by,
// so the URI is refused rather than handed out authenticating nothing.
func TestPairingURIWithoutAKeyPin(t *testing.T) {
	s := NewBootstrapServer(newFakeCAReader(t), 9472)
	s.SetPairingIssuer(&stubIssuer{pin: "none"}, 9470)

	if _, err := s.PairingURI("192.0.2.7"); err == nil {
		t.Fatal("PairingURI succeeded with no key pin")
	}
}

// Pairing is not on the bootstrap listener: that one is cleartext so a device
// can fetch the CA before it trusts anything, and a credential must not be
// issued over it.
func TestPairingIsNotOnTheBootstrapListener(t *testing.T) {
	s, issuer := newPairingServer(t)

	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/pair?pin="+s.PIN(), "application/json", nil)
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404: pairing is mounted on the agent's listener", resp.StatusCode)
	}
	if issuer.calls != 0 {
		t.Error("the bootstrap listener issued a credential")
	}
}

// A device scanning the QR is on the network, so the URI must name an address
// it can reach, not loopback.
func TestPairingURIPrefersARoutableHost(t *testing.T) {
	s, _ := newPairingServer(t)

	uri, err := s.PairingURI("")
	if err != nil {
		t.Fatalf("PairingURI: %v", err)
	}
	if ip := net.ParseIP(uri.Host); ip != nil && ip.IsLoopback() {
		hosts, _ := GetAllHosts()
		for _, h := range hosts {
			if candidate := net.ParseIP(h); candidate != nil && !candidate.IsLoopback() {
				t.Errorf("Host = %q, want a routable address such as %q", uri.Host, h)
				break
			}
		}
	}
}
