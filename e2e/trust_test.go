package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
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

	paired := pairDevice(t, h)
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
		t.Fatal("the paired device was not registered")
	}

	// The credential names the device, so what connects is what paired. An
	// identity minted per connection showed every paired device as offline.
	if deviceID != paired.DeviceID {
		t.Errorf("the device registered as %q, want the identity it paired with, %q", deviceID, paired.DeviceID)
	}

	online := h.Agent.OnlineDevices()
	if !slices.Contains(online, paired.DeviceID) {
		t.Errorf("OnlineDevices() = %v, want the paired device among them", online)
	}
}

// pairedDevice is what pairing hands a device.
type pairedDevice struct {
	DeviceID     string `json:"deviceID"`
	DeviceToken  string `json:"deviceToken"`
	PublicKeyPin string `json:"publicKeyPin"`
	AgentPort    int    `json:"agentPort"`
}

// pairDevice pairs one device as the mobile app does, with the PIN.
func pairDevice(t *testing.T, h *harness) pairedDevice {
	t.Helper()

	body, _ := json.Marshal(map[string]string{"deviceName": "Operator iPhone", "platform": "ios"})

	resp, err := http.Post(h.Pair+"/pair?pin="+h.Pairing.PIN(), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pairing status = %s, want 200", resp.Status)
	}

	var paired pairedDevice
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("decode the pairing response: %v", err)
	}
	return paired
}

// A revoked device is refused.
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

// A device that comes back holds the identity it paired with, so a second
// connection replaces the first rather than adding another device.
func TestAPairedDeviceReconnectsAsItself(t *testing.T) {
	h := start(t, options{Pairing: true})

	paired := pairDevice(t, h)

	// Left open deliberately: a phone whose radio dropped comes back while the
	// agent still holds a session it cannot reach.
	first, _, _ := h.phone(t, paired.DeviceToken, phoneCapabilities())
	second, secondID, _ := h.phone(t, paired.DeviceToken, phoneCapabilities())

	if secondID != paired.DeviceID {
		t.Errorf("the device came back as %q, want the identity it paired with, %q", secondID, paired.DeviceID)
	}

	_ = first.SetReadDeadline(time.Now().Add(timeout))
	_, _, err := first.ReadMessage()
	var timedOut net.Error
	if err == nil || (errors.As(err, &timedOut) && timedOut.Timeout()) {
		t.Errorf("the connection that was replaced is still being served (read: %v)", err)
	}

	// The replaced connection is torn down on its own goroutine, after this one
	// registered. What it drops must not be this one.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if online := h.Agent.OnlineDevices(); len(online) != 1 || online[0] != paired.DeviceID {
			t.Fatalf("OnlineDevices() = %v, want only the device that reconnected", online)
		}
		if err := h.Devices.SendToDevice(secondID, protocol.WebSocketMessage{Type: "ping"}); err != nil {
			t.Fatalf("the agent cannot reach the device that reconnected: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	send(t, second, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   secondID,
			"uid":        "04FEEDFACE",
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})
	if data := h.observed(t); data.Card == nil || data.Card.UID != "04:FE:ED:FA:CE" {
		t.Errorf("the agent saw %+v, want the scan from the device that reconnected", data)
	}
}
