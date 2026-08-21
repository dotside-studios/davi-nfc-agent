package deviceserver

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// respond runs one transceive request through the executor and returns the reply.
func respond(t *testing.T, s *Server, msg server.TransceiveRequestMessage) server.TransceiveResponseMessage {
	t.Helper()

	msg.ResponseCh = make(chan server.TransceiveResponseMessage, 1)
	go s.executeTransceiveRequest(msg)

	select {
	case resp := <-msg.ResponseCh:
		return resp
	case <-time.After(2 * time.Second):
		t.Fatal("executeTransceiveRequest did not answer within 2s")
		return server.TransceiveResponseMessage{}
	}
}

// A raw exchange can write to a config page or burn an OTP bit, and the agent
// cannot tell that from a SELECT, so read-only mode has to refuse it.
func TestTransceiveRefusedInReadOnlyMode(t *testing.T) {
	reader, err := nfc.NewNFCReader("", nfc.NewMockManager(), time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	defer reader.Stop()
	reader.SetMode(nfc.ModeReadOnly)

	s := New(Config{Reader: reader}, server.NewServerBridge())

	resp := respond(t, s, server.TransceiveRequestMessage{
		RequestID: "req-1",
		Data:      []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
	})

	if resp.Success {
		t.Fatal("read-only mode allowed a raw exchange")
	}
	if resp.ErrorCode != protocol.ErrCodeReadOnly {
		t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeReadOnly)
	}
}

func TestTransceiveWithoutReaderOrDevice(t *testing.T) {
	s := New(Config{}, server.NewServerBridge())

	resp := respond(t, s, server.TransceiveRequestMessage{
		RequestID: "req-2",
		TagUID:    "04:A1:B2:C3",
		Data:      []byte{0x30, 0x00},
	})

	if resp.Success {
		t.Fatal("succeeded with no reader and no device")
	}
	if resp.ErrorCode != protocol.ErrCodeNoCard {
		t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeNoCard)
	}
}

// Read/write mode must not refuse on mode grounds; it should get as far as
// looking for a tag and fail on that instead.
func TestTransceiveAllowedInReadWriteMode(t *testing.T) {
	reader, err := nfc.NewNFCReader("", nfc.NewMockManager(), time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	defer reader.Stop()
	reader.SetMode(nfc.ModeReadWrite)

	s := New(Config{Reader: reader}, server.NewServerBridge())

	resp := respond(t, s, server.TransceiveRequestMessage{
		RequestID: "req-3",
		Data:      []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
	})

	if resp.ErrorCode == protocol.ErrCodeReadOnly {
		t.Error("read/write mode was refused as read-only")
	}
}
