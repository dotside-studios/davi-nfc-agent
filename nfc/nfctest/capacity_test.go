package nfctest

import (
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestCapacity_FitsAcceptedOversizedRejected verifies the reader's pre-flight
// capacity check is wired and family-aware: a comfortably-fitting NDEF message
// writes and verifies, while a message larger than the tag's reported NDEF
// capacity is rejected with a capacity-exceeded error.
func TestCapacity_FitsAcceptedOversizedRejected(t *testing.T) {
	cards := []struct {
		name string
		make func() *EmulatedCard
	}{
		{"NTAG213", func() *EmulatedCard { return NTAG213("04A1B2C3D4E5F6") }},
		{"NTAG215", func() *EmulatedCard { return NTAG215("04A1B2C3D4E5F6") }},
		{"NTAG216", func() *EmulatedCard { return NTAG216("04A1B2C3D4E5F6") }},
		{"Ultralight", func() *EmulatedCard { return Ultralight("04A1B2C3D4E5F6") }},
		{"UltralightC", func() *EmulatedCard { return UltralightC("04A1B2C3D4E5F6") }},
		{"Classic1K", func() *EmulatedCard { return Classic1K("04112233") }},
	}
	for _, fc := range cards {
		t.Run(fc.name, func(t *testing.T) {
			card := fc.make()
			maxN := nfc.GetTagCapabilities(card.Tag()).MaxNDEFSize
			if maxN <= 0 {
				t.Skipf("%s reports no NDEF capacity", fc.name)
			}
			reader := NewEmulatedReader(t, card)

			// Comfortably-fitting write must verify.
			fit := textMessage(strings.Repeat("a", maxN/3))
			res, err := reader.WriteMessageWithResult(fit, nfc.WriteOptions{Overwrite: true, Index: -1})
			if err != nil {
				t.Fatalf("fitting write failed: %v", err)
			}
			if !res.Verified {
				t.Error("expected the fitting write to verify")
			}

			// Over-capacity write must be rejected with a capacity error.
			over := textMessage(strings.Repeat("b", maxN+64))
			if _, err := reader.WriteMessageWithResult(over, nfc.WriteOptions{Overwrite: true, Index: -1}); !nfc.IsCapacityExceededError(err) {
				t.Errorf("expected a capacity-exceeded error for an oversized write, got: %v", err)
			}
		})
	}
}
