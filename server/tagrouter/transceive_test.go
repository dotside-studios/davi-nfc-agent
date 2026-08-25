package tagrouter

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// codeOf reads the wire code a refusal carries.
func codeOf(err error) protocol.ErrorCode {
	if err == nil {
		return ""
	}
	return protocol.ErrorPayloadFor(err).Code
}

func readerInMode(t *testing.T, mode nfc.ReaderMode) *nfc.NFCReader {
	t.Helper()

	reader, err := nfc.NewNFCReader("", nfc.NewMockManager(), time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	t.Cleanup(reader.Stop)
	reader.SetMode(mode)
	return reader
}

// A raw exchange can write to a config page or burn an OTP bit, and the agent
// cannot tell that from a SELECT, so read-only mode has to refuse it.
func TestTransceiveRefusedInReadOnlyMode(t *testing.T) {
	s := New(Config{Reader: readerInMode(t, nfc.ModeReadOnly)})

	_, err := s.Transceive(context.Background(), server.TransceiveOp{
		Data: []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
	})
	if err == nil {
		t.Fatal("read-only mode allowed a raw exchange")
	}
	if got := codeOf(err); got != protocol.ErrCodeReadOnly {
		t.Errorf("errorCode = %q, want %q", got, protocol.ErrCodeReadOnly)
	}
}

func TestTransceiveWithoutReaderOrDevice(t *testing.T) {
	s := New(Config{})

	_, err := s.Transceive(context.Background(), server.TransceiveOp{
		Target: server.Target{TagUID: "04:A1:B2:C3"},
		Data:   []byte{0x30, 0x00},
	})
	if err == nil {
		t.Fatal("succeeded with no reader and no device")
	}
	if got := codeOf(err); got != protocol.ErrCodeNoCard {
		t.Errorf("errorCode = %q, want %q", got, protocol.ErrCodeNoCard)
	}
}

// Read/write mode must not refuse on mode grounds; it should get as far as
// looking for a tag and fail on that instead.
func TestTransceiveAllowedInReadWriteMode(t *testing.T) {
	s := New(Config{Reader: readerInMode(t, nfc.ModeReadWrite)})

	_, err := s.Transceive(context.Background(), server.TransceiveOp{
		Data: []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
	})
	if codeOf(err) == protocol.ErrCodeReadOnly {
		t.Error("read/write mode was refused as read-only")
	}
}
