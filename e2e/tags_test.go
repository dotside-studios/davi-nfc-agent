package e2e

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

const (
	readerUID = "04A1B2C3D4E5F6"
	phoneUID  = "04:A2:B3:C4"
)

// presentedTag is a writable tag on the agent's own reader.
func presentedTag(text string) *nfc.MockTag {
	tag := nfc.NewMockTag(readerUID)
	tag.TagType = "NTAG215"
	tag.IsConnected = true
	tag.Data = nfc.EncodeNdefMessageWithTextRecord(text, "en")
	return tag
}

// A card on the reader has to reach an application over the published protocol,
// and the same scan has to reach an observer registered through the library.
func TestAScanOnTheReaderReachesAClientAndAnObserver(t *testing.T) {
	h := start(t, options{Tags: []nfc.Tag{presentedTag("Hello, NFC!")}})
	conn := h.client(t)

	var payload struct {
		UID          string `json:"uid"`
		Type         string `json:"type"`
		Text         string `json:"text"`
		DeviceID     string `json:"deviceID"`
		Capabilities struct {
			CanWrite    bool `json:"canWrite"`
			CanLock     bool `json:"canLock"`
			MaxNDEFSize int  `json:"maxNdefSize"`
		} `json:"capabilities"`
	}
	decode(t, awaitFrame(t, conn, server.WSMessageTypeTagData).Payload, &payload)

	if payload.UID != readerUID {
		t.Errorf("uid = %q, want %q", payload.UID, readerUID)
	}
	if payload.Text != "Hello, NFC!" {
		t.Errorf("text = %q, want the record on the tag", payload.Text)
	}
	if payload.DeviceID != "" {
		t.Errorf("deviceID = %q, want it absent for the agent's own reader", payload.DeviceID)
	}
	if !payload.Capabilities.CanWrite || !payload.Capabilities.CanLock {
		t.Errorf("capabilities = %+v, want an NTAG215 reported as writable and lockable", payload.Capabilities)
	}
	if payload.Capabilities.MaxNDEFSize == 0 {
		t.Error("capabilities carried no maxNdefSize, so a client cannot size a write")
	}

	observed := h.observed(t)
	if observed.Card == nil || observed.Card.UID != readerUID {
		t.Errorf("the observer saw %+v, want the scanned card", observed.Card)
	}
}

// A client writes to the tag on the reader, and the bytes land on it.
func TestAClientWriteLandsOnTheTagInTheReader(t *testing.T) {
	tag := presentedTag("before")
	h := start(t, options{Tags: []nfc.Tag{tag}})
	conn := h.client(t)

	// Wait for the scan, so the write names a tag the agent knows is present.
	awaitFrame(t, conn, server.WSMessageTypeTagData)

	send(t, conn, protocol.WebSocketRequest{
		ID:   "write-1",
		Type: server.WSMessageTypeWriteRequest,
		Payload: map[string]any{
			"uid": readerUID,
			"records": []map[string]any{
				{"type": "text", "content": "after", "language": "en"},
			},
		},
	})

	reply := awaitReply(t, conn, "write-1")
	if !reply.Success {
		t.Fatalf("write refused: %s", reply.Error)
	}

	var result struct {
		UID      string `json:"uid"`
		TagType  string `json:"tagType"`
		Verified bool   `json:"verified"`
	}
	decode(t, reply.Payload, &result)
	if result.UID != readerUID {
		t.Errorf("the response named tag %q, want %q", result.UID, readerUID)
	}
	if !result.Verified {
		t.Error("the write was not verified, so the agent never read the data back")
	}

	stored, err := tag.ReadData()
	if err != nil {
		t.Fatalf("read the tag back: %v", err)
	}
	msg, err := nfc.DecodeNDEF(stored)
	if err != nil {
		t.Fatalf("the bytes on the tag are not an NDEF message: %v", err)
	}
	got, err := msg.GetText()
	if err != nil {
		t.Fatalf("the tag holds no text record: %v", err)
	}
	if got != "after" {
		t.Errorf("the tag holds %q, want the record the client wrote", got)
	}
}

// A phone scans a tag and reports it, and it reaches the client on the same
// endpoint the reader's own scans arrive on.
func TestAPhoneScanReachesAClient(t *testing.T) {
	h := start(t, options{})
	client := h.client(t)
	device, deviceID, _ := h.phone(t, apiSecret, phoneCapabilities())

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        phoneUID,
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})

	var payload struct {
		UID      string `json:"uid"`
		DeviceID string `json:"deviceID"`
	}
	decode(t, awaitFrame(t, client, server.WSMessageTypeTagData).Payload, &payload)

	if payload.UID != phoneUID {
		t.Errorf("uid = %q, want %q", payload.UID, phoneUID)
	}
	if payload.DeviceID != deviceID {
		t.Errorf("deviceID = %q, want the phone that scanned it (%q)", payload.DeviceID, deviceID)
	}
}

// The full cross-protocol loop: a client writes to a tag a phone is holding,
// the agent asks the phone to do it, and the phone's answer becomes the reply.
func TestAClientWriteRoutesToThePhoneHoldingTheTag(t *testing.T) {
	h := start(t, options{})
	client := h.client(t)
	device, deviceID, _ := h.phone(t, apiSecret, phoneCapabilities())

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        phoneUID,
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})
	awaitFrame(t, client, server.WSMessageTypeTagData)

	send(t, client, protocol.WebSocketRequest{
		ID:   "write-2",
		Type: server.WSMessageTypeWriteRequest,
		Payload: map[string]any{
			"uid":      phoneUID,
			"deviceID": deviceID,
			"records": []map[string]any{
				{"type": "uri", "content": "https://davi.social"},
			},
		},
	})

	forwarded := awaitFrame(t, device, remotenfc.WSTypeDeviceWriteRequest)
	var request struct {
		RequestID string `json:"requestID"`
		DeviceID  string `json:"deviceID"`
		TagUID    string `json:"tagUID"`
		NDEFBytes []byte `json:"ndefBytes"`
	}
	decode(t, forwarded.Payload, &request)

	if request.DeviceID != deviceID || request.TagUID != phoneUID {
		t.Errorf("the agent asked device %q for tag %q, want %q and %q",
			request.DeviceID, request.TagUID, deviceID, phoneUID)
	}
	if len(request.NDEFBytes) == 0 {
		t.Fatal("the write carried no encoded message for the phone to put on the tag")
	}

	msg, err := nfc.DecodeNDEF(request.NDEFBytes)
	if err != nil {
		t.Fatalf("the encoded message the phone was given is not valid NDEF: %v", err)
	}
	if uri, err := msg.GetURI(); err != nil || uri != "https://davi.social" {
		t.Errorf("the phone was given URI %q (%v), want the one the client sent", uri, err)
	}

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceWriteResponse,
		Payload: map[string]any{
			"requestID": request.RequestID,
			"success":   true,
		},
	})

	reply := awaitReply(t, client, "write-2")
	if !reply.Success {
		t.Fatalf("the client was told the write failed: %s", reply.Error)
	}

	// What the agent claims about a write it did not perform itself. The phone
	// confirms it wrote, but nothing read the tag back: its ReadData answers
	// with the snapshot taken at the scan, so a read-back would compare against
	// data that cannot have changed. Reporting verified here would be a claim
	// the agent has no basis for.
	var result struct {
		UID      string `json:"uid"`
		Verified bool   `json:"verified"`
		Locked   bool   `json:"locked"`
	}
	decode(t, reply.Payload, &result)

	if result.UID != phoneUID {
		t.Errorf("the response named tag %q, want the phone's %q", result.UID, phoneUID)
	}
	if result.Verified {
		t.Error("the write was reported verified, but nothing read the tag back")
	}
	if result.Locked {
		t.Error("the write was reported locked, but the client did not ask for one")
	}
}

// A raw exchange with a tag a phone is holding, base64 in both directions.
func TestAClientTransceiveRoutesToThePhone(t *testing.T) {
	h := start(t, options{RawAPDU: true})
	client := h.client(t)
	device, deviceID, _ := h.phone(t, apiSecret, phoneCapabilities())

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        phoneUID,
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})
	awaitFrame(t, client, server.WSMessageTypeTagData)

	send(t, client, protocol.WebSocketRequest{
		ID:   "apdu-1",
		Type: server.WSMessageTypeTransceiveRequest,
		Payload: map[string]any{
			"uid":      phoneUID,
			"deviceID": deviceID,
			"data":     base64.StdEncoding.EncodeToString([]byte{0x00, 0xA4, 0x04, 0x00}),
		},
	})

	forwarded := awaitFrame(t, device, remotenfc.WSTypeDeviceTransceiveRequest)
	var request struct {
		RequestID string `json:"requestID"`
		Data      []byte `json:"data"`
	}
	decode(t, forwarded.Payload, &request)
	if len(request.Data) != 4 {
		t.Errorf("the phone was sent %d command bytes, want the 4 the client sent", len(request.Data))
	}

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceTransceiveResponse,
		Payload: map[string]any{
			"requestID": request.RequestID,
			"success":   true,
			"data":      []byte{0x90, 0x00},
		},
	})

	reply := awaitReply(t, client, "apdu-1")
	if !reply.Success {
		t.Fatalf("the client was told the exchange failed: %s", reply.Error)
	}

	var response struct {
		Data []byte `json:"data"`
	}
	decode(t, reply.Payload, &response)
	if len(response.Data) != 2 || response.Data[0] != 0x90 {
		t.Errorf("the client received %v, want the phone's 90 00", response.Data)
	}
}

// Read-only mode is the agent's, not the tag's, and it has to reach a write
// aimed at a phone as well as one aimed at the reader.
func TestReadOnlyModeRefusesAWriteToAPhone(t *testing.T) {
	h := start(t, options{})
	client := h.client(t)
	device, deviceID, _ := h.phone(t, apiSecret, phoneCapabilities())

	send(t, device, protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        phoneUID,
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})
	awaitFrame(t, client, server.WSMessageTypeTagData)

	h.Agent.Supervisor().SetMode(nfc.ModeReadOnly)

	send(t, client, protocol.WebSocketRequest{
		ID:   "write-3",
		Type: server.WSMessageTypeWriteRequest,
		Payload: map[string]any{
			"uid":      phoneUID,
			"deviceID": deviceID,
			"records": []map[string]any{
				{"type": "text", "content": "should not land"},
			},
		},
	})

	reply := awaitReply(t, client, "write-3")
	if reply.Success {
		t.Fatal("read-only mode admitted a write to a tag held by a phone")
	}
}
