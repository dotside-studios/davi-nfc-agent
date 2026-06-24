package nfctest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// These tests close the one gap the access-bit emulator alone can't cover: that
// a real phone would recognize a formatted MIFARE Classic card as an NDEF tag.
// They do it by reading the formatted card the way a phone's NFC stack does —
// independently of the production format/read code — and confirming the NDEF
// message round-trips. If this reconstructs what was written, a compliant phone
// will too. (MAD/AID interpretation is the phone-side logic being emulated here;
// only the physical RF tap remains for a real-hardware smoke test.)

// phoneMADCRC computes the MIFARE Application Directory CRC the way a phone
// validates it (CRC-8, polynomial 0x1D, preset 0xC7), reimplemented here so the
// check is independent of the production writer.
func phoneMADCRC(data []byte) byte {
	const poly = 0x1D
	crc := byte(0xC7)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// formatClassicForTest formats a blank Classic 1K with the given encoded NDEF
// bytes via ForceInitialize and returns the tag, driven directly (no reader, so
// no background poll races the subsequent phone-style read).
func formatClassicForTest(t *testing.T, ndef []byte) nfc.ClassicTag {
	t.Helper()
	tag := nfc.NewEmulatedTag(newClassicEmulator(), "04AABBCCDD", nfc.DetectedClassic1K)
	aw, ok := tag.(nfc.AdvancedWriter)
	if !ok {
		t.Fatal("Classic tag does not implement AdvancedWriter")
	}
	if err := aw.WriteDataWithOptions(ndef, nfc.TagWriteOptions{ForceInitialize: true}); err != nil {
		t.Fatalf("ForceInitialize format: %v", err)
	}
	return tag.(nfc.ClassicTag)
}

// phoneReadClassicNDEF reads the NDEF message from a formatted Classic card the
// way a phone would: authenticate sector 0 with the MAD key and read the MAD,
// verify its CRC, follow the AID 0x03E1 entries to the NDEF sectors, read those
// with the NFC Forum public key, then parse the NDEF TLV. Returns the raw NDEF
// message bytes (after confirming they decode).
func phoneReadClassicNDEF(t *testing.T, classic nfc.ClassicTag) []byte {
	t.Helper()

	// 1. Read the MAD (sector 0, blocks 1-2) using the MAD key.
	mad1, err := classic.Read(0, 1, nfc.KeyMAD, nfc.KeyTypeA)
	if err != nil {
		t.Fatalf("phone read: MAD block 1 with MAD key: %v", err)
	}
	mad2, err := classic.Read(0, 2, nfc.KeyMAD, nfc.KeyTypeA)
	if err != nil {
		t.Fatalf("phone read: MAD block 2 with MAD key: %v", err)
	}
	mad := append(append([]byte(nil), mad1...), mad2...)
	if len(mad) < 32 {
		t.Fatalf("phone read: short MAD (%d bytes)", len(mad))
	}

	// 2. Verify the MAD CRC (byte 0 covers the info byte + the 15 AID entries).
	if got := phoneMADCRC(mad[1:32]); got != mad[0] {
		t.Fatalf("phone read: MAD CRC mismatch: computed 0x%02X, stored 0x%02X", got, mad[0])
	}

	// 3. Follow the AID entries to the NDEF sectors (NFC Forum AID 0x03E1).
	var ndefSectors []int
	for s := 1; s <= 15; s++ {
		if mad[2*s] == 0x03 && mad[2*s+1] == 0xE1 {
			ndefSectors = append(ndefSectors, s)
		}
	}
	if len(ndefSectors) == 0 {
		t.Fatal("phone read: MAD lists no NDEF sectors")
	}

	// 4. Read the NDEF sectors' data blocks with the NFC Forum public key.
	var raw []byte
	for _, s := range ndefSectors {
		for b := 0; b < 3; b++ {
			blk, err := classic.Read(uint8(s), uint8(b), nfc.KeyNFCForum, nfc.KeyTypeA)
			if err != nil {
				t.Fatalf("phone read: sector %d block %d with NFC Forum key: %v", s, b, err)
			}
			raw = append(raw, blk...)
		}
	}

	// 5. Extract and decode the NDEF message.
	ndefBytes, found := nfc.TLVFindNDEF(raw)
	if !found {
		t.Fatal("phone read: no NDEF TLV found in the data sectors")
	}
	if _, err := nfc.DecodeNDEF(ndefBytes); err != nil {
		t.Fatalf("phone read: NDEF does not decode: %v", err)
	}
	return ndefBytes
}

func TestPhone_ReadsFormattedClassicURI(t *testing.T) {
	want, err := (&nfc.NDEFMessageBuilder{
		Records: []nfc.NDEFRecordBuilder{&nfc.NDEFURI{Content: "https://example.com/nfc"}},
	}).Build()
	if err != nil {
		t.Fatalf("build NDEF: %v", err)
	}
	wantBytes, err := want.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}

	classic := formatClassicForTest(t, wantBytes)
	got := phoneReadClassicNDEF(t, classic)

	if !bytes.Equal(got, wantBytes) {
		t.Errorf("phone-read NDEF differs from what was written:\n want % X\n got  % X", wantBytes, got)
	}
}

func TestPhone_ReadsFormattedClassicMultiSector(t *testing.T) {
	// ~150 bytes of text spans several data sectors, exercising MAD-directed
	// multi-sector reassembly the way a phone walks the AID entries.
	want := textMessage(strings.Repeat("ndef-", 30))
	wantBytes, err := want.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}

	classic := formatClassicForTest(t, wantBytes)
	got := phoneReadClassicNDEF(t, classic)

	if !bytes.Equal(got, wantBytes) {
		t.Errorf("phone-read multi-sector NDEF differs (%d vs %d bytes):\n want % X\n got  % X",
			len(got), len(wantBytes), wantBytes, got)
	}
}
