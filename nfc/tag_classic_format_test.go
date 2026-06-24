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

// TestValidateNDEFTrailer_CanonicalAreSafe verifies the trailers the formatter
// actually writes are both consistent and recoverable (rewritable with Key B).
func TestValidateNDEFTrailer_CanonicalAreSafe(t *testing.T) {
	if err := validateNDEFTrailer(madSectorTrailer); err != nil {
		t.Errorf("MAD trailer should be safe to write: %v", err)
	}
	if err := validateNDEFTrailer(ndefSectorTrailer); err != nil {
		t.Errorf("NDEF trailer should be safe to write: %v", err)
	}
}

// TestValidateNDEFTrailer_RejectsUnsafe verifies the guard rejects trailers that
// would brick a sector: inconsistent access bits, and consistent-but-not-
// recoverable conditions (e.g. the factory transport config, recoverable only
// with Key A, which formatting changes to a public key).
func TestValidateNDEFTrailer_RejectsUnsafe(t *testing.T) {
	tests := []struct {
		name       string
		b6, b7, b8 byte
	}{
		{"inconsistent all-zero", 0x00, 0x00, 0x00},
		{"inconsistent FF FF FF", 0xFF, 0xFF, 0xFF},
		{"transport config (cond 001)", 0xFF, 0x07, 0x80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trailer := make([]byte, 16)
			trailer[6], trailer[7], trailer[8] = tt.b6, tt.b7, tt.b8
			if err := validateNDEFTrailer(trailer); err == nil {
				t.Errorf("expected validateNDEFTrailer to reject %02X %02X %02X", tt.b6, tt.b7, tt.b8)
			}
		})
	}
}

// TestTrailerAccessCondition decodes the canonical trailers and confirms both
// resolve to the recoverable access condition.
func TestTrailerAccessCondition(t *testing.T) {
	for _, tr := range [][]byte{madSectorTrailer, ndefSectorTrailer} {
		if got := trailerAccessCondition(tr[6], tr[7], tr[8]); got != trailerCondRecoverable {
			t.Errorf("trailer access condition = %03b, want %03b", got, trailerCondRecoverable)
		}
	}
}
