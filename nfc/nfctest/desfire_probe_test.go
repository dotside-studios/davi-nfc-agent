package nfctest

import (
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Memory size and NDEF capacity vary per card, so the profile carries neither
// and the driver reads them off the card on its first operation.
func TestDESFire_ProbeReportsMemoryAndCapacity(t *testing.T) {
	tag := DESFire("04DE5F1RE0").Tag()

	before := nfc.GetTagCapabilities(tag)
	if before.MemorySize != 0 || before.MaxNDEFSize != 0 {
		t.Errorf("before the first read: MemorySize = %d, MaxNDEFSize = %d, want 0 and 0",
			before.MemorySize, before.MaxNDEFSize)
	}

	if err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00}); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	after := nfc.GetTagCapabilities(tag)
	if after.MemorySize != 8192 {
		t.Errorf("MemorySize = %d, want 8192 (storage byte 0x1A)", after.MemorySize)
	}
	if after.MaxNDEFSize != 254 {
		t.Errorf("MaxNDEFSize = %d, want 254 (a 256-byte file less the NLEN prefix)", after.MaxNDEFSize)
	}
	if after.TagFamily != "DESFire EV2" {
		t.Errorf("TagFamily = %q, want %q", after.TagFamily, "DESFire EV2")
	}
	if after.CanWrite != true || after.IsReadOnly {
		t.Errorf("CanWrite = %v, IsReadOnly = %v, want true and false", after.CanWrite, after.IsReadOnly)
	}
}

// The generation is in the family, not the name, so a card-type filter naming
// "DESFire" keeps matching every generation.
func TestDESFire_TypeIsTheFamilyName(t *testing.T) {
	if got := DESFire("04DE5F1RE0").Tag().Type(); got != nfc.CardTypeDesfire {
		t.Errorf("Type() = %q, want %q", got, nfc.CardTypeDesfire)
	}
}

// An NDEF file written only by a key the driver does not hold is read-only to
// it. The write is refused before any byte reaches the card.
func TestDESFire_KeyProtectedFileIsReadOnly(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").KeyProtected().Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}

	caps := nfc.GetTagCapabilities(tag)
	if caps.CanWrite || !caps.IsReadOnly {
		t.Errorf("CanWrite = %v, IsReadOnly = %v, want false and true", caps.CanWrite, caps.IsReadOnly)
	}
	if writable, err := tag.IsWritable(); err != nil || writable {
		t.Errorf("IsWritable() = (%v, %v), want (false, nil)", writable, err)
	}

	err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00})
	if !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData() = %v, want a read-only error", err)
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}

// The probed capacity bounds the write, so a message too large for the file is
// refused rather than written partway.
func TestDESFire_WriteBeyondTheFileIsRefused(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}

	err := tag.WriteData(make([]byte, 255))
	if !nfc.IsCapacityExceededError(err) {
		t.Errorf("WriteData(255 bytes into a 254-byte capacity) = %v, want a capacity error", err)
	}
	if err != nil && !strings.Contains(err.Error(), "254") {
		t.Errorf("error names no capacity: %v", err)
	}
}

// A card that refuses the probe is driven with the kind's defaults rather than
// failing: no capacity is checked, and no memory size is claimed.
func TestDESFire_UnprobableCardKeepsTheProfileDefaults(t *testing.T) {
	e := newDESFireEmulator()
	e.version = nil
	tag := nfc.NewEmulatedTag(e, "04DE5F1RE0", nfc.DetectedDESFireEV1)

	if err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00}); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	caps := nfc.GetTagCapabilities(tag)
	if caps.MemorySize != 0 {
		t.Errorf("MemorySize = %d, want 0 for a card whose version did not parse", caps.MemorySize)
	}
	if caps.TagFamily != "DESFire EV1" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "DESFire EV1")
	}
}
