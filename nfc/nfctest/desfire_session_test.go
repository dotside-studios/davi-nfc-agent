package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// heldKeys is what the agent would be configured with for a card provisioned
// the way KeyProtected provisions one.
func heldKeys() nfc.DESFireKeys {
	return nfc.DESFireKeys{DESFireWriteKeyNo: DESFireKey}
}

// A file behind a key the agent holds is written through a session: the driver
// authenticates, and every command it sends from then on is MACed and counted.
func TestDESFireSession_WriteWithTheKeyHeld(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode ev2.CommMode
	}{
		{"plain", ev2.CommPlain},
		{"mac", ev2.CommMAC},
		{"full", ev2.CommFull},
	} {
		t.Run(mode.name, func(t *testing.T) {
			card := DESFire("04DE5F1RE0").ReadKeyProtected(mode.mode).WithDESFireKeys(heldKeys())
			tag := card.Tag()

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

// A payload larger than one command's budget is written as several, each its
// own MACed exchange at its own offset, so nothing depends on frame chaining.
func TestDESFireSession_RoundTripsAcrossCommandBoundaries(t *testing.T) {
	for _, size := range []int{1, 15, 16, 17, 31, 32, 33, 64, 128, 254} {
		card := DESFire("04DE5F1RE0").ReadKeyProtected(ev2.CommFull).WithDESFireKeys(heldKeys())
		tag := card.Tag()

		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*7 + 3)
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

// The wrong key does not open the file. The card refuses the authentication,
// and the driver reports it rather than writing anything.
func TestDESFireSession_WrongKeyIsRefused(t *testing.T) {
	wrong := nfc.DESFireKeys{DESFireWriteKeyNo: bytes.Repeat([]byte{0x22}, 16)}
	tag := DESFire("04DE5F1RE0").KeyProtected(ev2.CommMAC).WithDESFireKeys(wrong).Tag()

	err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00})
	if err == nil {
		t.Fatal("a write went through under the wrong key")
	}
	if !nfc.IsAuthError(err) {
		t.Errorf("WriteData: %v, want an authentication error", err)
	}
}

// A card whose file is behind a key the agent does not hold reports itself
// read-only, and the write is refused before any byte reaches it.
func TestDESFireSession_NoKeyHeldIsReadOnly(t *testing.T) {
	tag := DESFire("04DE5F1RE0").KeyProtected(ev2.CommMAC).Tag()

	if _, err := tag.ReadData(); !nfc.IsNoPayloadError(err) {
		t.Fatalf("ReadData: %v, want a no-payload error", err)
	}
	if caps := nfc.GetTagCapabilities(tag); caps.CanWrite || !caps.IsReadOnly {
		t.Errorf("CanWrite = %v, IsReadOnly = %v, want false and true", caps.CanWrite, caps.IsReadOnly)
	}
	if err := tag.WriteData([]byte{0xD1}); !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData: %v, want a read-only error", err)
	}
	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}

// A free file is still driven plainly, so holding keys costs a card that does
// not need them nothing.
func TestDESFireSession_FreeFileNeedsNoAuthentication(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithDESFireKeys(heldKeys()).Tag()

	payload := []byte{0xD1, 0x01, 0x01, 0x54, 0x00}
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
}
