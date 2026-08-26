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
