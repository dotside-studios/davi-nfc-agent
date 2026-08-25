package tagrouter

import (
	"context"
	"errors"
	"strings"
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

// configInMode is the router as the agent builds it, over a supervisor in the
// given mode.
func configInMode(t *testing.T, mode nfc.ReaderMode) Config {
	t.Helper()
	return configOver(t, nfc.NewMockManager(), mode)
}

// configOver is the router as the agent builds it, over a supervisor of the
// given manager in the given mode.
func configOver(t *testing.T, manager nfc.Manager, mode nfc.ReaderMode) Config {
	t.Helper()

	readers := supervisorOf(t, manager, mode)
	return Config{
		Tags:                 readers,
		AllowTagModification: func() bool { return readers.Mode() != nfc.ModeReadOnly },
	}
}

func supervisorOf(t *testing.T, manager nfc.Manager, mode nfc.ReaderMode) *nfc.Supervisor {
	t.Helper()

	readers, err := nfc.NewSupervisor(manager, time.Second)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := readers.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(readers.Stop)
	readers.SetMode(mode)
	return readers
}

// A raw exchange can write to a config page or burn an OTP bit, and the agent
// cannot tell that from a SELECT, so read-only mode has to refuse it.
func TestTransceiveRefusedInReadOnlyMode(t *testing.T) {
	s := New(configInMode(t, nfc.ModeReadOnly))

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
	s := New(configInMode(t, nfc.ModeReadWrite))

	_, err := s.Transceive(context.Background(), server.TransceiveOp{
		Data: []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
	})
	if codeOf(err) == protocol.ErrCodeReadOnly {
		t.Error("read/write mode was refused as read-only")
	}
}

// A tag the reader could not read is still a tag to operate on: a blank or
// damaged one is exactly what a client asks to write, and it never reaches the
// reader's last scan. So an untargeted request routes to the reader holding it,
// which works against the tag it actually has.
func TestUntargetedWriteReachesAReaderHoldingATagItCouldNotRead(t *testing.T) {
	m := nfc.NewMockManager()
	m.MockDevice.SetTags([]nfc.Tag{nfc.NewMockTag("04A1B2C3")}) // never connected, so reads fail
	s := New(configOver(t, m, nfc.ModeReadWrite))

	awaitCardOnReader(t, s.config.Tags, "mock:usb:001")

	_, err := s.Write(context.Background(), server.WriteOp{
		Target:  server.Target{AllowUntargeted: true},
		Request: server.WriteRequest{Records: []server.WriteRecord{{Type: "text", Content: "hello"}}},
	})
	if err == nil {
		t.Fatal("a write succeeded against a tag the reader cannot read")
	}
	if got := codeOf(err); got == protocol.ErrCodeNoCard {
		t.Error("the router found nothing to route to, with a card on the reader")
	}
	if !strings.Contains(err.Error(), "04A1B2C3") {
		t.Errorf("the failure is %q, want the reader reporting on the tag it has", err)
	}
}

// With nothing on any reader there is nothing to guess at, and saying so is a
// better answer than a reader's own complaint about a card it does not have.
func TestUntargetedExchangeIsRefusedWhenNothingHoldsATag(t *testing.T) {
	s := New(configInMode(t, nfc.ModeReadWrite))

	_, err := s.Transceive(context.Background(), server.TransceiveOp{
		Target: server.Target{AllowUntargeted: true},
		Data:   []byte{0x30, 0x00},
	})
	if got := codeOf(err); got != protocol.ErrCodeNoCard {
		t.Errorf("errorCode = %q, want %q", got, protocol.ErrCodeNoCard)
	}
}

// A source that explains itself is reported in its own words and its own code.
// One that does not is reported as the operation failing, which is all the
// router knows: a reader with no card on it has not gone anywhere.
func TestASourceFailureIsReportedInTheSourcesTerms(t *testing.T) {
	silent := sourceFailure(errors.New("no card detected"), "mock:usb:001", "exchange", protocol.ErrCodeTransceiveFailed)
	if got := codeOf(silent); got != protocol.ErrCodeTransceiveFailed {
		t.Errorf("errorCode = %q, want %q", got, protocol.ErrCodeTransceiveFailed)
	}
	if !strings.Contains(silent.Error(), "mock:usb:001") {
		t.Errorf("the failure is %q, want it to name the source", silent)
	}

	spoken := protocol.Errorf(protocol.ErrCodeTagRemoved, "the tag was lifted")
	if got := codeOf(sourceFailure(spoken, "mock:usb:001", "exchange", protocol.ErrCodeTransceiveFailed)); got != protocol.ErrCodeTagRemoved {
		t.Errorf("errorCode = %q, want the source's own %q", got, protocol.ErrCodeTagRemoved)
	}
}

// awaitCardOnReader waits for the reader to report the tag on it, so an
// operation does not overtake the poll it depends on.
func awaitCardOnReader(t *testing.T, tags nfc.TagHolder, device string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := tags.TagOn(device); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reader %s never reported the tag on it", device)
}

// A request naming both a device and a tag is held to that tag even when the
// source cannot name the one it is holding: the caller said which tag it means,
// and the source checks the one it actually has before touching it.
func TestANamedTagIsEnforcedOnASourceThatCannotNameItsOwn(t *testing.T) {
	m := nfc.NewMockManager()
	m.MockDevice.SetTags([]nfc.Tag{nfc.NewMockTag("04A1B2C3")}) // never connected, so reads fail
	s := New(configOver(t, m, nfc.ModeReadWrite))

	awaitCardOnReader(t, s.config.Tags, "mock:usb:001")

	_, err := s.Write(context.Background(), server.WriteOp{
		Target:  server.Target{DeviceID: "mock:usb:001", TagUID: "04FFFFFF"},
		Request: server.WriteRequest{Records: []server.WriteRecord{{Type: "text", Content: "hello"}}},
	})
	if err == nil {
		t.Fatal("a write reached a tag the request did not name")
	}
	if !errors.Is(err, nfc.ErrTagUIDMismatch) {
		t.Errorf("err = %v, want the write refused for naming another tag", err)
	}
}
