package nfctest

import (
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// countingTransport records how many commands reached it, so a test can prove a
// rejected command never touched the emulated silicon.
type countingTransport struct {
	calls int
}

func (c *countingTransport) IsCardPresent() bool { return true }

func (c *countingTransport) Transceive(cmd []byte) ([]byte, error) {
	c.calls++
	return []byte{0x90, 0x00}, nil
}

func TestStrictTransport(t *testing.T) {
	inner := &countingTransport{}
	st := strictTransport{inner}

	// A well-formed command passes through to the emulator.
	if _, err := st.Transceive([]byte{0xFF, 0xB0, 0x00, 0x04, 0x04}); err != nil {
		t.Errorf("well-formed READ was rejected: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("well-formed command reached the emulator %d times, want 1", inner.calls)
	}

	// A malformed command — Lc claims 5 data bytes, 3 follow — is refused before
	// it reaches the emulator.
	inner.calls = 0
	if _, err := st.Transceive([]byte{0x00, 0xA4, 0x04, 0x00, 0x05, 0xE1, 0x03, 0x00}); err == nil {
		t.Error("a malformed APDU was not rejected")
	}
	if inner.calls != 0 {
		t.Error("a malformed APDU reached the emulator; it should have been stopped first")
	}
}

// The wrapper only judges framing, never meaning: a command it does not
// recognise is not its to refuse — an emulator answers those itself.
func TestStrictTransportPassesUnrecognisedCommands(t *testing.T) {
	inner := &countingTransport{}
	st := strictTransport{inner}
	if _, err := st.Transceive([]byte{0x00, 0xEE, 0x00, 0x00}); err != nil {
		t.Errorf("an unrecognised but well-formed command was rejected: %v", err)
	}
	if inner.calls != 1 {
		t.Error("an unrecognised command should still reach the emulator")
	}
}

// Every emulated card round-trips through the strict wrapper, so the production
// drivers must not build a malformed APDU for any tag type. A failure here is a
// driver framing bug, not an ordinary read/write outcome.
func TestEmulatorsDriveWellFormedAPDUs(t *testing.T) {
	msg, err := (&nfc.NDEFMessageBuilder{
		Records: []nfc.NDEFRecordBuilder{&nfc.NDEFText{Content: "strict", Language: "en"}},
	}).Build()
	if err != nil {
		t.Fatalf("build NDEF: %v", err)
	}
	data, err := msg.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}

	cards := []*EmulatedCard{
		NTAG213("04A1B2C3D4E5F6"),
		NTAG215("04A1B2C3D4E5F7"),
		Ultralight("04AABBCCDDEEFF"),
		Classic1K("0453C9A17F2280"),
		DESFire("04E81D3B6A4400"),
		Type4("04117BE2C15D91"),
	}
	for _, card := range cards {
		card := card
		t.Run(card.UID(), func(t *testing.T) {
			// Both paths build APDUs: write is selects + updates, read is
			// selects + reads. Only a malformed-APDU error is this test's
			// concern; any other outcome is an ordinary one.
			if err := card.Tag().WriteData(data); isMalformed(err) {
				t.Errorf("write built a malformed APDU: %v", err)
			}
			if _, err := card.Tag().ReadData(); isMalformed(err) {
				t.Errorf("read built a malformed APDU: %v", err)
			}
		})
	}
}

func isMalformed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "malformed APDU")
}
