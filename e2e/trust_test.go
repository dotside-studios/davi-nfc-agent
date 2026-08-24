package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// A device recognises the agent by its key, not by an authority. The pin it is
// told at registration has to be the key the handshake actually produced, or a
// device that records it can never connect again.
func TestTheAgentServesTheKeyItReportsToDevices(t *testing.T) {
	h := start(t, options{})

	// The dial verifies the served certificate against Agent.PublicKeyPin.
	_, deviceID, reported := h.phone(t, apiSecret, phoneCapabilities())
	if deviceID == "" {
		t.Fatal("the device was not registered")
	}
	if reported != h.Agent.PublicKeyPin() {
		t.Errorf("registration reported pin %q, want %q", reported, h.Agent.PublicKeyPin())
	}
}

func TestHealthAnswersOverTLS(t *testing.T) {
	h := start(t, options{})

	resp, err := h.httpClient(t).Get(h.Origin + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200", resp.Status)
	}

	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode the health payload: %v", err)
	}
	if health.Status == "" {
		t.Error("the health check reported no status")
	}
}

// Pairing end to end: a device presents the PIN, is issued its own credential,
// and that credential is what admits it once only paired devices are allowed.
func TestPairingIssuesTheCredentialThatAdmitsADevice(t *testing.T) {
	h := start(t, options{Pairing: true})

	pin := h.Pairing.PIN()
	body, _ := json.Marshal(map[string]string{"deviceName": "Operator iPhone", "platform": "ios"})

	resp, err := http.Post(h.Pair+"/pair?pin="+pin, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pairing status = %s, want 200", resp.Status)
	}

	var paired struct {
		DeviceID     string `json:"deviceID"`
		DeviceToken  string `json:"deviceToken"`
		PublicKeyPin string `json:"publicKeyPin"`
		AgentPort    int    `json:"agentPort"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("decode the pairing response: %v", err)
	}
	if paired.DeviceToken == "" {
		t.Fatal("pairing issued no token")
	}
	if paired.PublicKeyPin != h.Agent.PublicKeyPin() {
		t.Errorf("pairing reported pin %q, want %q", paired.PublicKeyPin, h.Agent.PublicKeyPin())
	}
	if paired.AgentPort != h.Agent.DevicePort() {
		t.Errorf("pairing pointed the device at port %d, want %d", paired.AgentPort, h.Agent.DevicePort())
	}

	// Withdraw the shared secret and the loopback bypass, which is what the
	// tray toggle and -require-paired-devices do.
	h.Agent.SetRequirePairedDevice(true)

	if _, _, err := h.dial(t, "/ws?mode=device&secret="+apiSecret, nil); err == nil {
		t.Error("a device holding only the shared secret was admitted while pairing was required")
	}

	_, deviceID, _ := h.phone(t, paired.DeviceToken, phoneCapabilities())
	if deviceID == "" {
		t.Error("the paired device was not registered")
	}
}

// A revoked device is refused, which is the point of per-device credentials.
func TestARevokedDeviceIsRefused(t *testing.T) {
	h := start(t, options{Pairing: true})

	pin := h.Pairing.PIN()
	resp, err := http.Post(h.Pair+"/pair?pin="+pin, "application/json", nil)
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var paired struct {
		DeviceID    string `json:"deviceID"`
		DeviceToken string `json:"deviceToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("decode the pairing response: %v", err)
	}

	h.Agent.SetRequirePairedDevice(true)
	if err := h.Agent.Devices().Revoke(paired.DeviceID); err != nil {
		t.Fatalf("revoke the device: %v", err)
	}

	if _, _, err := h.dial(t, "/ws?mode=device&secret="+paired.DeviceToken, nil); err == nil {
		t.Error("a revoked device was still admitted")
	}
}

// The wrong PIN is refused, so a device cannot pair itself without one.
func TestPairingRefusesTheWrongPIN(t *testing.T) {
	h := start(t, options{Pairing: true})

	resp, err := http.Post(h.Pair+"/pair?pin=000000", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Error("pairing accepted a PIN it never issued")
	}
}
