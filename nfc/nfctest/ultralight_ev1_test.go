package nfctest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestUltralightEV1_RoundTripBeyond48Bytes writes a payload too large for an
// original Ultralight onto a 128-byte EV1. The driver used to size every
// non-C Ultralight at 48 bytes, so this would have been refused.
func TestUltralightEV1_RoundTripBeyond48Bytes(t *testing.T) {
	e := newUltralightEV1Emulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralightEV1_128)

	want := buildMessage(t, &nfc.NDEFText{Content: strings.Repeat("ev1 ", 20)})
	if len(want) <= 48 {
		t.Fatalf("test payload is %d bytes, expected it to exceed an Ultralight", len(want))
	}

	if err := tag.WriteData(want); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("round-trip mismatch:\ngot  % X\nwant % X", got, want)
	}
}

// TestUltralightEV1_RefusesPartialLock checks that a 128-byte EV1 reports it
// cannot be made read-only, and refuses when asked. Setting only the static
// lock bytes would leave pages 16-35 writable, and lock bits cannot be undone.
func TestUltralightEV1_RefusesPartialLock(t *testing.T) {
	e := newUltralightEV1Emulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralightEV1_128)

	if caps := nfc.GetTagCapabilities(tag); caps.CanLock {
		t.Error("capabilities report the 128-byte EV1 as lockable")
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly error = %v, want a not-supported error", err)
	}
	if !e.tryWrite(4, []byte{1, 2, 3, 4}) {
		t.Error("page 4 was locked despite the refusal")
	}
}
