package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestClassic4K_LowLevelAddressing round-trips block reads/writes across both
// the small sectors (0-31, 4 blocks) and the large sectors (32-39, 16 blocks) of
// a 4K card, exercising the driver's 4K sector/block addressing and the
// emulator's matching geometry.
func TestClassic4K_LowLevelAddressing(t *testing.T) {
	e := newClassic4KEmulator()
	tag := nfc.NewEmulatedTag(e, "044B1240", nfc.DetectedClassic4K)
	classic, ok := tag.(nfc.ClassicTag)
	if !ok {
		t.Fatal("expected a ClassicTag")
	}

	cases := []struct{ sector, block uint8 }{
		{1, 0}, {15, 2}, {31, 0}, // small sectors
		{32, 0}, {35, 10}, {39, 14}, // large sectors (16 blocks each)
	}
	for _, c := range cases {
		data := make([]byte, 16)
		for i := range data {
			data[i] = byte(int(c.sector)*16 + int(c.block) + i)
		}
		if err := classic.Write(c.sector, c.block, data, nfc.KeyDefault, nfc.KeyTypeA); err != nil {
			t.Fatalf("write sector %d block %d: %v", c.sector, c.block, err)
		}
		got, err := classic.Read(c.sector, c.block, nfc.KeyDefault, nfc.KeyTypeA)
		if err != nil {
			t.Fatalf("read sector %d block %d: %v", c.sector, c.block, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("sector %d block %d round-trip mismatch:\n want % X\n got  % X", c.sector, c.block, data, got)
		}
	}
}

// TestClassic4K_LargeSectorIsolation verifies that the 16-block large sectors are
// addressed independently — writing one block doesn't bleed into a neighbour in
// the same large sector or an adjacent large sector.
func TestClassic4K_LargeSectorIsolation(t *testing.T) {
	e := newClassic4KEmulator()
	tag := nfc.NewEmulatedTag(e, "044B1240", nfc.DetectedClassic4K)
	classic := tag.(nfc.ClassicTag)

	a := bytes.Repeat([]byte{0xAA}, 16)
	b := bytes.Repeat([]byte{0xBB}, 16)
	if err := classic.Write(32, 0, a, nfc.KeyDefault, nfc.KeyTypeA); err != nil {
		t.Fatalf("write 32/0: %v", err)
	}
	if err := classic.Write(32, 14, b, nfc.KeyDefault, nfc.KeyTypeA); err != nil {
		t.Fatalf("write 32/14: %v", err)
	}

	got0, _ := classic.Read(32, 0, nfc.KeyDefault, nfc.KeyTypeA)
	got14, _ := classic.Read(32, 14, nfc.KeyDefault, nfc.KeyTypeA)
	if !bytes.Equal(got0, a) || !bytes.Equal(got14, b) {
		t.Errorf("large-sector blocks not isolated: 32/0=% X 32/14=% X", got0, got14)
	}
}

// TestClassic4K_HighLevelLargePayload writes an NDEF payload big enough to spill
// past block 128 into the large sectors, then reads it back — validating the
// full ReadData/WriteData pipeline over 4K geometry (trailer skipping and
// per-sector authentication across the 1K/4K boundary).
func TestClassic4K_HighLevelLargePayload(t *testing.T) {
	e := newClassic4KEmulator()
	tag := nfc.NewEmulatedTag(e, "044B1240", nfc.DetectedClassic4K)

	// Small sectors 1-31 hold ~1440 bytes; 1500 forces data into the large
	// sectors (blocks >= 128).
	payload := make([]byte, 1500)
	for i := range payload {
		payload[i] = byte(i*7 + 1)
	}
	if err := tag.WriteData(payload); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("4K large-payload round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}
