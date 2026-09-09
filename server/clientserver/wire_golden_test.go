package clientserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// These fixtures are the client protocol as it is actually served: every
// message a browser client can receive, marshalled and compared byte for byte
// against testdata/. Most of these payloads are built as map literals with no
// Go type behind them, so nothing else says what their field names are, and a
// rename or a dropped key is invisible until a client goes quiet. The
// deviceStatus casing bug reached a release that way.
//
// A failure here is a wire change. If it was deliberate, run
//
//	go test ./server/clientserver -update
//
// and commit the fixtures with the change, so the diff shows what clients see.

var updateGolden = flag.Bool("update", false, "rewrite the golden wire fixtures")

// scannedAt is fixed so the fixtures do not move with the clock.
var scannedAt = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

// golden compares one message against testdata/<name>.json.
//
// The comparison is on what the JSON means, not on its bytes: the message is
// marshalled, decoded and marshalled again, so every fixture comes out with
// sorted keys. A map and a struct with the same fields serialise in different
// orders, and without this a refactor from one to the other churns every
// fixture on the ordering and buries whatever really changed.
func golden(t *testing.T, name string, msg any) {
	t.Helper()

	got, err := normalizeJSON(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	path := filepath.Join("testdata", name+".json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s changed.\n got: %s\nwant: %s\n\nIf this is a deliberate wire change, rerun with -update and commit the fixture.",
			path, got, want)
	}
}

// normalizeJSON renders a message with its keys sorted. UseNumber keeps an
// integer an integer through the round trip; absent and null stay distinct,
// since a missing key decodes to no key at all.
func normalizeJSON(msg any) ([]byte, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var round any
	if err := dec.Decode(&round); err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(round, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// scannedCard builds a card with a fixed timestamp, so what varies between
// fixtures is the payload and not the clock.
func scannedCard(uid string) *nfc.Card {
	card := nfc.NewCard(nfc.NewMockTag(uid))
	card.ScannedAt = scannedAt
	card.LastAccessed = scannedAt
	return card
}

// fixedOps answers every operation with the same canned result, so a response
// fixture describes the shape the server puts on the wire rather than anything
// a reader decided.
type fixedOps struct {
	stoppedOps
}

func (fixedOps) Write(context.Context, server.WriteOp) (*nfc.WriteResult, error) {
	return &nfc.WriteResult{
		UID:          "04A1B2C3",
		TagType:      "NTAG215",
		BytesWritten: 42,
		Verified:     true,
		Attempts:     1,
		Locked:       false,
	}, nil
}

func (fixedOps) Lock(context.Context, server.LockOp) (*nfc.LockResult, error) {
	return &nfc.LockResult{UID: "04A1B2C3", TagType: "NTAG215", Locked: true}, nil
}

func (fixedOps) Transceive(context.Context, server.TransceiveOp) ([]byte, error) {
	return []byte{0x04, 0xA2, 0xB3, 0x90, 0x00}, nil
}

func (fixedOps) Capabilities(context.Context, server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	caps := nfc.GetTagCapabilities(nfc.NewMockTag("04A1B2C3"))
	return &caps, nil
}

// nilResultOps reports a successful write with no result. The response is a
// different shape from a write that reports one, and a build supplying its own
// Ops can produce it, so it is part of the contract.
type nilResultOps struct {
	stoppedOps
}

func (nilResultOps) Write(context.Context, server.WriteOp) (*nfc.WriteResult, error) {
	return nil, nil
}

// respondTo sends one request and returns the reply as the client sees it.
func respondTo(t *testing.T, request map[string]any) map[string]any {
	t.Helper()
	return respondWith(t, fixedOps{}, request)
}

func respondWith(t *testing.T, ops server.TagOps, request map[string]any) map[string]any {
	t.Helper()

	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("write: %v", err)
	}
	return readResponse(t, conn)
}

func TestGoldenTagDataWithACard(t *testing.T) {
	golden(t, "tagdata_card", tagDataMessage(nfc.NFCData{Card: scannedCard("04A1B2C3")}))
}

// The message sub-object is a hand-built union of two shapes and has no Go type
// at all, so these two fixtures are the only statement of what it looks like.
func TestGoldenTagDataWithAnNDEFMessage(t *testing.T) {
	card := scannedCard("04A1B2C3")
	ndef := nfc.NewNDEFMessage()
	ndef.AddText("hello", "en")
	card.MessageData = ndef

	golden(t, "tagdata_ndef", tagDataMessage(nfc.NFCData{Card: card}))
}

func TestGoldenTagDataWithARawMessage(t *testing.T) {
	card := scannedCard("04A1B2C3")
	card.MessageData = nfc.NewTextMessage([]byte{0x01, 0x02, 0x03})

	golden(t, "tagdata_raw", tagDataMessage(nfc.NFCData{Card: card}))
}

// A tag whose driver names no type still carries the keys, empty. They are not
// dropped, which is what an omitempty on them would do.
func TestGoldenTagDataWithAnUnnamedType(t *testing.T) {
	tag := nfc.NewMockTag("04A1B2C3")
	tag.TagType = ""
	card := nfc.NewCard(tag)
	card.ScannedAt = scannedAt

	golden(t, "tagdata_untyped", tagDataMessage(nfc.NFCData{Card: card}))
}

// A scan with no card is how the agent reports the tag leaving the field. It is
// a different shape from the one above, not the same shape with empty values.
func TestGoldenTagDataOnRemoval(t *testing.T) {
	golden(t, "tagdata_removed", tagDataMessage(nfc.NFCData{}))
}

func TestGoldenDeviceStatus(t *testing.T) {
	golden(t, "devicestatus", protocol.WebSocketMessage{
		Type: server.WSMessageTypeDeviceStatus,
		Payload: nfc.DeviceStatus{
			Device:      "mock:usb:001",
			Connected:   true,
			Message:     "Reader ready",
			CardPresent: false,
		},
	})
}

func TestGoldenWriteResponse(t *testing.T) {
	golden(t, "writeresponse", respondTo(t, map[string]any{
		"id":   "req-write",
		"type": "writeRequest",
		"payload": map[string]any{
			"uid":     "04A1B2C3",
			"records": []any{map[string]any{"type": "text", "content": "hello"}},
		},
	}))
}

// A write that lands but reports nothing carries the acknowledgement alone.
func TestGoldenWriteResponseWithNoResult(t *testing.T) {
	golden(t, "writeresponse_noresult", respondWith(t, nilResultOps{}, map[string]any{
		"id":   "req-write",
		"type": "writeRequest",
		"payload": map[string]any{
			"uid":     "04A1B2C3",
			"records": []any{map[string]any{"type": "text", "content": "hello"}},
		},
	}))
}

func TestGoldenLockResponse(t *testing.T) {
	golden(t, "lockresponse", respondTo(t, map[string]any{
		"id":      "req-lock",
		"type":    "lockRequest",
		"payload": map[string]any{"uid": "04A1B2C3"},
	}))
}

func TestGoldenCapabilitiesResponse(t *testing.T) {
	golden(t, "capabilitiesresponse", respondTo(t, map[string]any{
		"id":      "req-caps",
		"type":    "capabilitiesRequest",
		"payload": map[string]any{"uid": "04A1B2C3"},
	}))
}

func TestGoldenTransceiveResponse(t *testing.T) {
	golden(t, "transceiveresponse", respondTo(t, map[string]any{
		"id":      "req-transceive",
		"type":    "transceiveRequest",
		"payload": map[string]any{"uid": "04A1B2C3", "data": "/8oAAAA="},
	}))
}

// Clients switch on payload.code and payload.retryable, so both the envelope
// and the payload are part of the contract.
func TestGoldenErrorResponse(t *testing.T) {
	golden(t, "error", respondTo(t, map[string]any{
		"id":      "req-unknown",
		"type":    "somethingElse",
		"payload": map[string]any{},
	}))
}
