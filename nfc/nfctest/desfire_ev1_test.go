package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev1"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// An EV1 does not answer AuthenticateEV2First, so a file behind a key on one was
// out of reach until the older exchange existed. It is reachable now.
func TestDESFireEV1_WriteWithTheKeyHeld(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode ev2.CommMode
	}{
		{"plain", ev2.CommPlain},
		{"mac", ev2.CommMAC},
	} {
		t.Run(mode.name, func(t *testing.T) {
			tag := DESFireEV1("04DE5F1RE1").ReadKeyProtected(mode.mode).
				WithDESFireKeys(heldKeys()).Tag()

			if _, err := tag.ReadData(); !nfc.IsNoPayloadError(err) {
				t.Fatalf("ReadData on a blank card: %v, want a no-payload error", err)
			}
			if !nfc.GetTagCapabilities(tag).CanWrite {
				t.Fatal("CanWrite is false with the write key held")
			}

			payload := []byte{0xD1, 0x01, 0x05, 0x54, 0x02, 'e', 'n', 'h', 'i'}
			if err := tag.WriteData(payload); err != nil {
				t.Fatalf("WriteData: %v", err)
			}

			got, err := tag.ReadData()
			if err != nil {
				t.Fatalf("ReadData: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("round trip: got % X, want % X", got, payload)
			}
		})
	}
}

// The chain binds a whole sequence, so a transfer spanning several commands
// stays verified across all of them.
func TestDESFireEV1_RoundTripsAcrossCommandBoundaries(t *testing.T) {
	for _, size := range []int{1, 31, 32, 33, 64, 254} {
		tag := DESFireEV1("04DE5F1RE1").ReadKeyProtected(ev2.CommMAC).
			WithDESFireKeys(heldKeys()).Tag()

		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*5 + 2)
		}

		if err := tag.WriteData(payload); err != nil {
			t.Fatalf("WriteData(%d bytes): %v", size, err)
		}
		got, err := tag.ReadData()
		if err != nil {
			t.Fatalf("ReadData(%d bytes): %v", size, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("%d-byte round trip mismatch:\n got % X\nwant % X", size, got, payload)
		}
	}
}

// The driver picks the exchange from the generation the card reported, so an
// EV1 is never offered the newer one and an EV2 is never offered the older.
func TestDESFireEV1_UsesTheOlderExchange(t *testing.T) {
	tag := DESFireEV1("04DE5F1RE1").KeyProtected(ev2.CommMAC).
		WithDESFireKeys(heldKeys()).Tag()

	if err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00}); err != nil {
		t.Fatalf("WriteData on an EV1: %v", err)
	}

	// And the newer card still uses the newer one.
	ev2Tag := DESFire("04DE5F1RE2").KeyProtected(ev2.CommMAC).
		WithDESFireKeys(heldKeys()).Tag()
	if err := ev2Tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00}); err != nil {
		t.Fatalf("WriteData on an EV2: %v", err)
	}
}

// The wrong key does not open the file, and nothing is written.
func TestDESFireEV1_WrongKeyIsRefused(t *testing.T) {
	wrong := nfc.DESFireKeys{DESFireWriteKeyNo: bytes.Repeat([]byte{0x33}, 16)}
	tag := DESFireEV1("04DE5F1RE1").KeyProtected(ev2.CommMAC).WithDESFireKeys(wrong).Tag()

	err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00})
	if !nfc.IsAuthError(err) {
		t.Errorf("WriteData under the wrong key = %v, want an authentication error", err)
	}
}

// Enciphered communication is not implemented for this generation, and a file
// asking for it is refused rather than driven with the wrong scheme.
func TestDESFireEV1_EncipheredFileIsRefused(t *testing.T) {
	tag := DESFireEV1("04DE5F1RE1").KeyProtected(ev2.CommFull).
		WithDESFireKeys(heldKeys()).Tag()

	err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00})
	if err == nil {
		t.Fatal("an enciphered EV1 file was written")
	}
	if !bytes.Contains([]byte(err.Error()), []byte(ev1.ErrEnciphered.Error())) {
		t.Errorf("WriteData = %v, want it to name the unimplemented mode", err)
	}
}
