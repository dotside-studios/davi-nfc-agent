package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// removable is implemented by every emulator: it makes the modeled card leave
// the field after a given number of transceive operations.
type removable interface {
	setRemoveAfter(n int)
}

// removalCases builds a fresh tag + its emulator for each tag family.
func removalCases() []struct {
	name string
	make func() (nfc.Tag, removable)
} {
	return []struct {
		name string
		make func() (nfc.Tag, removable)
	}{
		{"NTAG215", func() (nfc.Tag, removable) {
			e := newNTAGEmulator(nfc.DetectedNTAG215)
			return nfc.NewEmulatedTag(e, "04A1B2C3D4E5F6", nfc.DetectedNTAG215), e
		}},
		{"Ultralight", func() (nfc.Tag, removable) {
			e := newUltralightEmulator()
			return nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralight), e
		}},
		{"Classic1K", func() (nfc.Tag, removable) {
			e := newClassicEmulator()
			return nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K), e
		}},
		{"DESFire", func() (nfc.Tag, removable) {
			e := newDESFireEmulator()
			return nfc.NewEmulatedTag(e, "04DE5F1RE0", nfc.DetectedDESFire), e
		}},
	}
}

// TestRemoval_DuringReadReturnsCardRemoved verifies every driver surfaces a
// typed card-removed error (not a generic failure or a partial success) when the
// card leaves the field part-way through a read.
func TestRemoval_DuringReadReturnsCardRemoved(t *testing.T) {
	for _, tc := range removalCases() {
		t.Run(tc.name, func(t *testing.T) {
			tag, rem := tc.make()
			rem.setRemoveAfter(2) // leave the field a couple of operations in

			_, err := tag.ReadData()
			if err == nil {
				t.Fatal("expected an error when the card is removed mid-read")
			}
			if !nfc.IsCardRemovedError(err) {
				t.Errorf("expected a card-removed error, got: %v", err)
			}
		})
	}
}

// TestRemoval_DuringWriteReturnsCardRemoved verifies the same for writes: a card
// pulled mid-write yields a typed card-removed error rather than a silent or
// misclassified failure.
func TestRemoval_DuringWriteReturnsCardRemoved(t *testing.T) {
	for _, tc := range removalCases() {
		t.Run(tc.name, func(t *testing.T) {
			tag, rem := tc.make()
			rem.setRemoveAfter(2)

			err := tag.WriteData(sampleNDEF)
			if err == nil {
				t.Fatal("expected an error when the card is removed mid-write")
			}
			if !nfc.IsCardRemovedError(err) {
				t.Errorf("expected a card-removed error, got: %v", err)
			}
		})
	}
}
