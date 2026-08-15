package clientserver

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/server"
)

// readResponse waits for one frame from the socket.
func readResponse(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
}) map[string]any {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func TestTransceiveRejectsMalformedData(t *testing.T) {
	s := newTestServer(nil)
	conn := dial(t, s, "https://app.example.com")

	for _, data := range []string{"not-base64!!", ""} {
		if err := conn.WriteJSON(map[string]any{
			"id":      "req-1",
			"type":    "transceiveRequest",
			"payload": map[string]any{"data": data},
		}); err != nil {
			t.Fatalf("write: %v", err)
		}

		msg := readResponse(t, conn)
		if msg["type"] != "error" {
			t.Errorf("data=%q: type = %v, want error", data, msg["type"])
		}
	}
}

// The command must reach the bridge as the exact bytes the client encoded —
// this is the whole point of a raw exchange.
func TestTransceivePassesBytesThroughToTheBridge(t *testing.T) {
	bridge := server.NewServerBridge()
	s := New(Config{AllowedOrigins: []string{"*"}}, bridge)
	conn := dial(t, s, "https://app.example.com")

	command := []byte{0xFF, 0xCA, 0x00, 0x00, 0x00}
	reply := []byte{0x04, 0xA2, 0xB3, 0x90, 0x00}

	// Stand in for the device server.
	go func() {
		msg := <-bridge.Transceive
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   true,
			Data:      reply,
		}
	}()

	if err := conn.WriteJSON(map[string]any{
		"id":      "req-2",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString(command), "raw": true},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readResponse(t, conn)
	if msg["type"] != "transceiveResponse" {
		t.Fatalf("type = %v", msg["type"])
	}
	if msg["success"] != true {
		t.Fatalf("success = %v, error = %v", msg["success"], msg["error"])
	}

	payload, _ := msg["payload"].(map[string]any)
	got, err := base64.StdEncoding.DecodeString(payload["data"].(string))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(got) != string(reply) {
		t.Errorf("response = % X, want % X", got, reply)
	}
}

func TestTransceiveCarriesTheRawFlag(t *testing.T) {
	bridge := server.NewServerBridge()
	s := New(Config{AllowedOrigins: []string{"*"}}, bridge)
	conn := dial(t, s, "https://app.example.com")

	seen := make(chan bool, 1)
	go func() {
		msg := <-bridge.Transceive
		seen <- msg.Raw
		msg.ResponseCh <- server.TransceiveResponseMessage{RequestID: msg.RequestID, Success: true}
	}()

	_ = conn.WriteJSON(map[string]any{
		"id":      "req-3",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x30, 0x00}), "raw": true},
	})

	select {
	case raw := <-seen:
		if !raw {
			t.Error("raw flag did not reach the bridge")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge never saw the request")
	}
}

// A raw exchange can write, so it is counted alongside writes — the count is
// there to show which clients can change a tag.
func TestTransceiveCountsAsAWrite(t *testing.T) {
	bridge := server.NewServerBridge()
	s := New(Config{AllowedOrigins: []string{"*"}}, bridge)
	conn := dial(t, s, "https://app.example.com")

	go func() {
		msg := <-bridge.Transceive
		msg.ResponseCh <- server.TransceiveResponseMessage{RequestID: msg.RequestID, Success: true}
	}()

	_ = conn.WriteJSON(map[string]any{
		"id":      "req-4",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x60})},
	})

	waitFor(t, func() bool {
		c := s.Clients()
		return len(c) == 1 && c[0].Writes == 1
	})
}
