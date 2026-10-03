package clientserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"sync"
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

// The command must reach the tag as the exact bytes the client encoded: this is
// the whole point of a raw exchange.
func TestTransceivePassesBytesThroughToTheOperation(t *testing.T) {
	command := []byte{0xFF, 0xCA, 0x00, 0x00, 0x00}
	reply := []byte{0x04, 0xA2, 0xB3, 0x90, 0x00}

	ops := &echoOps{reply: reply, seen: make(chan server.TransceiveOp, 4)}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

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
	ops := &echoOps{seen: make(chan server.TransceiveOp, 4)}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id":      "req-3",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x30, 0x00}), "raw": true},
	})

	select {
	case op := <-ops.seen:
		if !op.Raw {
			t.Error("raw flag did not reach the operation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the operation never saw the request")
	}
}

// A raw exchange can write, so it is counted alongside writes. The count is
// there to show which clients can change a tag.
func TestTransceiveCountsAsAWrite(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: &echoOps{seen: make(chan server.TransceiveOp, 1)}})
	conn := dial(t, s, "https://app.example.com")

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

// The whole reply comes back byte for byte whatever its status word, with the
// status word split out for an APDU-level exchange.
func TestTransceiveReturnsEveryStatusWordWithSuccess(t *testing.T) {
	replies := []struct {
		reply []byte
		sw    string
		body  []byte
	}{
		{append(bytes.Repeat([]byte{0xAB}, 16), 0x91, 0xAF), "91AF", bytes.Repeat([]byte{0xAB}, 16)},
		{[]byte{0x91, 0x00}, "9100", nil},
		{[]byte{0x6A, 0x82}, "6A82", nil},
		{[]byte{0x01, 0x90, 0x00}, "9000", []byte{0x01}},
	}

	for _, tc := range replies {
		ops := &echoOps{reply: tc.reply, seen: make(chan server.TransceiveOp, 4)}
		s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
		conn := dial(t, s, "https://app.example.com")

		_ = conn.WriteJSON(map[string]any{
			"id":      "req-sw",
			"type":    "transceiveRequest",
			"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x90, 0x71, 0x00, 0x00, 0x00})},
		})

		msg := readResponse(t, conn)
		if msg["success"] != true {
			t.Fatalf("reply % X: success = %v, error = %v", tc.reply, msg["success"], msg["error"])
		}
		payload, _ := msg["payload"].(map[string]any)
		got, _ := base64.StdEncoding.DecodeString(payload["data"].(string))
		if !bytes.Equal(got, tc.reply) {
			t.Errorf("data = % X, want % X", got, tc.reply)
		}
		if payload["sw"] != tc.sw {
			t.Errorf("reply % X: sw = %v, want %s", tc.reply, payload["sw"], tc.sw)
		}
		encoded, present := payload["body"].(string)
		if tc.body == nil {
			if present {
				t.Errorf("reply % X: body = %v, want it omitted", tc.reply, encoded)
			}
			continue
		}
		body, _ := base64.StdEncoding.DecodeString(encoded)
		if !bytes.Equal(body, tc.body) {
			t.Errorf("reply % X: body = % X, want % X", tc.reply, body, tc.body)
		}
	}
}

func TestTransceiveRawFramingCarriesNoStatusWord(t *testing.T) {
	ops := &echoOps{reply: []byte{0x01, 0x02, 0x03}, seen: make(chan server.TransceiveOp, 4)}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id":      "req-rawframe",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x30, 0x00}), "raw": true},
	})

	payload, _ := readResponse(t, conn)["payload"].(map[string]any)
	if _, present := payload["sw"]; present {
		t.Errorf("sw = %v on a framing-level reply, want it omitted", payload["sw"])
	}
}

func TestRawSessionBeginTransceiveEnd(t *testing.T) {
	ops := &sessionOps{echoOps: echoOps{reply: []byte{0x91, 0x00}, seen: make(chan server.TransceiveOp, 4)}}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id":      "b",
		"type":    "rawSessionBeginRequest",
		"payload": map[string]any{"uid": "04A1B2C3", "ttlMs": 2000},
	})
	begin := readResponse(t, conn)
	if begin["type"] != "rawSessionBeginResponse" || begin["success"] != true {
		t.Fatalf("begin = %v", begin)
	}
	payload, _ := begin["payload"].(map[string]any)
	if payload["sessionId"] != "lease-1" || payload["expiresInMs"] != float64(2000) {
		t.Errorf("begin payload = %v", payload)
	}
	if ops.begun.UID() != "04A1B2C3" || ops.begun.ttl != 2*time.Second {
		t.Errorf("operation saw uid %q ttl %v", ops.begun.UID(), ops.begun.ttl)
	}

	_ = conn.WriteJSON(map[string]any{
		"id":      "t",
		"type":    "transceiveRequest",
		"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte{0x90, 0xAF}), "sessionId": "lease-1"},
	})
	if msg := readResponse(t, conn); msg["success"] != true {
		t.Fatalf("transceive = %v", msg)
	}
	select {
	case op := <-ops.seen:
		if op.SessionID != "lease-1" {
			t.Errorf("session id = %q, want lease-1", op.SessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the operation never saw the exchange")
	}

	_ = conn.WriteJSON(map[string]any{
		"id":      "e",
		"type":    "rawSessionEndRequest",
		"payload": map[string]any{"sessionId": "lease-1"},
	})
	if msg := readResponse(t, conn); msg["type"] != "rawSessionEndResponse" || msg["success"] != true {
		t.Fatalf("end = %v", msg)
	}
	if got := ops.endedIDs(); len(got) != 1 || got[0] != "lease-1" {
		t.Errorf("ended = %v, want [lease-1]", got)
	}
}

func TestRawSessionEndsWhenTheClientDisconnects(t *testing.T) {
	ops := &sessionOps{echoOps: echoOps{seen: make(chan server.TransceiveOp, 4)}}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id":      "b",
		"type":    "rawSessionBeginRequest",
		"payload": map[string]any{"uid": "04A1B2C3"},
	})
	readResponse(t, conn)

	_ = conn.Close()
	waitFor(t, func() bool { return len(ops.endedIDs()) == 1 })
}

func TestRawSessionNotSupportedByTheOperationLayer(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: &echoOps{seen: make(chan server.TransceiveOp, 1)}})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id":      "b",
		"type":    "rawSessionBeginRequest",
		"payload": map[string]any{"uid": "04A1B2C3"},
	})
	msg := readResponse(t, conn)
	if msg["type"] != "error" {
		t.Errorf("type = %v, want error", msg["type"])
	}
}

// sessionOps adds raw sessions to echoOps.
type sessionOps struct {
	echoOps

	mu    sync.Mutex
	begun beginSeen
	ended []string
}

type beginSeen struct {
	op  server.Target
	ttl time.Duration
}

func (b beginSeen) UID() string { return b.op.TagUID }

func (o *sessionOps) BeginRawSession(_ context.Context, req server.RawSessionBeginOp) (server.RawSessionLease, error) {
	o.mu.Lock()
	o.begun = beginSeen{op: req.Target, ttl: req.TTL}
	o.mu.Unlock()
	ttl := req.TTL
	if ttl == 0 {
		ttl = 5 * time.Second
	}
	return server.RawSessionLease{SessionID: "lease-1", TTL: ttl}, nil
}

func (o *sessionOps) EndRawSession(_ context.Context, id string) error {
	o.mu.Lock()
	o.ended = append(o.ended, id)
	o.mu.Unlock()
	return nil
}

func (o *sessionOps) endedIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.ended...)
}

// echoOps answers a raw exchange with a fixed reply and records what it was
// asked for.
type echoOps struct {
	stoppedOps
	reply []byte
	seen  chan server.TransceiveOp
}

func (o *echoOps) Transceive(_ context.Context, req server.TransceiveOp) ([]byte, error) {
	select {
	case o.seen <- req:
	default:
	}
	return o.reply, nil
}
