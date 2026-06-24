package nfc

import "testing"

// TestMADCRC_AllNDEF is a known-answer test for the MIFARE Application Directory
// CRC. A MAD1 marking all 15 data sectors as NFC Forum NDEF (info byte 0x01
// followed by 15 AID entries of 0x03 0xE1) has a well-documented CRC of 0x14 —
// the value seen in real NDEF-formatted MIFARE Classic 1K dumps (sector 0,
// block 1, byte 0). This pins the CRC polynomial/preset without hardware.
func TestMADCRC_AllNDEF(t *testing.T) {
	input := []byte{madInfoByte}
	for i := 0; i < 15; i++ {
		input = append(input, madAIDNDEFLo, madAIDNDEFHi)
	}

	if got := madCRC(input); got != 0x14 {
		t.Errorf("madCRC(all-NDEF MAD1) = 0x%02X, want 0x14", got)
	}
}

// TestClassicFormatConstants guards the canonical NDEF-format trailer layout
// (KeyA(6) | AccessBits(3) | GPB(1) | KeyB(6)) against accidental edits.
func TestClassicFormatConstants(t *testing.T) {
	if len(madSectorTrailer) != 16 || len(ndefSectorTrailer) != 16 {
		t.Fatalf("sector trailers must be 16 bytes: mad=%d ndef=%d",
			len(madSectorTrailer), len(ndefSectorTrailer))
	}
	if madSectorTrailer[9] != 0xC1 {
		t.Errorf("MAD GPB = 0x%02X, want 0xC1", madSectorTrailer[9])
	}
	if ndefSectorTrailer[9] != 0x40 {
		t.Errorf("NDEF GPB = 0x%02X, want 0x40", ndefSectorTrailer[9])
	}
}
