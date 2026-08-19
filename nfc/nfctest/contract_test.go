package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestReaderTagsKeepTheContract runs every tag family the reader path can
// produce through the shared contract, against the emulators that stand in for
// the silicon.
func TestReaderTagsKeepTheContract(t *testing.T) {
	for _, fam := range allFamilies() {
		t.Run(fam.name, func(t *testing.T) {
			AssertTagContract(t, fam.make())
		})
	}
}

// TestUltralightEV1TagsKeepTheContract covers the EV1 variants, which
// allFamilies does not build: the 128-byte one is the only reader tag that
// reports it cannot be locked.
func TestUltralightEV1TagsKeepTheContract(t *testing.T) {
	tests := []struct {
		name string
		make func() nfc.Tag
	}{
		{"UltralightEV1", func() nfc.Tag {
			return nfc.NewEmulatedTag(newUltralightEmulator(), "04AABBCCDDEEFF", nfc.DetectedUltralightEV1)
		}},
		{"UltralightEV1_128", func() nfc.Tag {
			return nfc.NewEmulatedTag(newUltralightEV1Emulator(), "04AABBCCDDEEFF", nfc.DetectedUltralightEV1_128)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AssertTagContract(t, tt.make())
		})
	}
}
