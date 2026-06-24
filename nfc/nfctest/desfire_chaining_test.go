package nfctest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestDESFire_ChainingBoundaries round-trips payloads at and around the 59-byte
// native frame size, where DESFire read/write must split into chained frames
// (0xAF continuations). Sizes bracket each frame boundary and the file capacity
// (256-byte file minus the 2-byte NLEN prefix = 254 usable).
func TestDESFire_ChainingBoundaries(t *testing.T) {
	sizes := []int{1, 58, 59, 60, 117, 118, 119, 177, 200, 254}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("%dB", n), func(t *testing.T) {
			e := newDESFireEmulator()
			tag := nfc.NewEmulatedTag(e, "04DE5F1RE0", nfc.DetectedDESFire)

			payload := make([]byte, n)
			for i := range payload {
				payload[i] = byte(i*3 + 1)
			}
			if err := tag.WriteData(payload); err != nil {
				t.Fatalf("WriteData(%d bytes): %v", n, err)
			}
			got, err := tag.ReadData()
			if err != nil {
				t.Fatalf("ReadData(%d bytes): %v", n, err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("%d-byte chained round-trip mismatch:\n want % X\n got  % X", n, payload, got)
			}
		})
	}
}
