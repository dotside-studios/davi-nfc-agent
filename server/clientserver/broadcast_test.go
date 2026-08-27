package clientserver

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// A scan handed to the server reaches the clients connected to it. What decides
// which scans get here is the agent's; this end only serves them.
func TestBroadcastReachesAClient(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}})
	conn := dial(t, s, "https://app.example.com")

	s.Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04DEADBE"))})

	msg := readResponse(t, conn)
	payload, _ := msg["payload"].(map[string]any)
	if uid, _ := payload["uid"].(string); uid != "04DEADBE" {
		t.Errorf("the client received %#v, want the scan", msg)
	}
}

// A server with nobody connected drops the scan rather than failing.
func TestBroadcastWithNoClientsIsFine(t *testing.T) {
	New(Config{AllowedOrigins: []string{"*"}}).
		Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04000001"))})
}

// The deviceStatus payload is the shape docs/api.md documents and the client
// library reads. It is nfc.DeviceStatus marshalled directly, so the field names
// are that struct's json tags: without them Go exported the Go names and every
// client read undefined, which left the client library's "the reader has no
// card, so forget the tag it was holding" branch dead.
func TestDeviceStatusIsBroadcastInTheDocumentedShape(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}})
	conn := dial(t, s, "https://app.example.com")

	s.BroadcastDeviceStatus(nfc.DeviceStatus{
		Device:      "mock:usb:001",
		Connected:   true,
		Message:     "Reader ready",
		CardPresent: false,
	})

	msg := readResponse(t, conn)
	payload, _ := msg["payload"].(map[string]any)

	want := map[string]any{
		"device":      "mock:usb:001",
		"connected":   true,
		"message":     "Reader ready",
		"cardPresent": false,
	}
	for key, value := range want {
		got, ok := payload[key]
		if !ok {
			t.Errorf("payload has no %q: %#v", key, payload)
			continue
		}
		if got != value {
			t.Errorf("payload[%q] = %#v, want %#v", key, got, value)
		}
	}
}
