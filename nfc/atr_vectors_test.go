package nfc

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Differential vectors for ATR card-name detection, taken from the ATR registry
// in pcsc-tools (LudovicRousseau/pcsc-tools, smartcard_list.txt), which follows
// the PC/SC Part 3 supplemental document and is the database pcsc_scan prints
// card names from.
//
// Detection constants are the part of this package with nothing behind them:
// there is no round trip to fail and no emulator to disagree, because the
// emulators are built from a kind that detection already decided. So they are
// pinned here against a source that is not this package.
//
// The registry writes the standard byte as a wildcard for these cards, which is
// why the parser does not read it: the byte varies by reader while the card name
// after it does not.

// atrFromRegistry parses an ATR written as the registry writes one, with ".."
// for a byte that varies.
func atrFromRegistry(t *testing.T, s string) []byte {
	t.Helper()

	var out []byte
	for _, field := range strings.Fields(s) {
		if field == ".." {
			out = append(out, 0x00)
			continue
		}
		b, err := hex.DecodeString(field)
		if err != nil || len(b) != 1 {
			t.Fatalf("bad ATR byte %q", field)
		}
		out = append(out, b[0])
	}
	return out
}

func TestATRCardNameVectors(t *testing.T) {
	tests := []struct {
		atr  string
		want DetectedTagType
		// name is how the registry labels the entry.
		name string
	}{
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 01 00 00 00 00 ..",
			DetectedClassic1K, "MIFARE Classic 1K"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 02 00 00 00 00 ..",
			DetectedClassic4K, "MIFARE Classic 4K"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 03 00 00 00 00 ..",
			DetectedUltralight, "MIFARE Ultralight"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 26 00 00 00 00 ..",
			DetectedMini, "Mifare Mini"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 11 00 3B 00 00 00 00 42",
			DetectedFeliCa, "RFID - FeliCa (generic), and the Suica family"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 03 FF 20 00 00 00 00 B4",
			DetectedDESFire, "MIFARE DESFire EV3 4K"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectTagTypeFromATR(atrFromRegistry(t, tt.atr)); got != tt.want {
				t.Errorf("DetectTagTypeFromATR = %v, want %v\nATR    %s\nsource pcsc-tools smartcard_list.txt: %q",
					got, tt.want, tt.atr, tt.name)
			}
		})
	}
}

// Names this table used to carry that the registry says belong to other cards.
// None of them named what this package thought, and one of them sent a MIFARE
// Mini down the DESFire path.
func TestATRNamesThatWereWrong(t *testing.T) {
	tests := []struct {
		atr string
		// was is what this package used to answer.
		was string
		// is is what the registry says the name belongs to.
		is string
	}{
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 04 00 00 00 00 ..",
			"MIFARE Mini", "SLE55R_XXXX"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 06 00 00 00 00 ..",
			"MIFARE Plus 2K SL1", "SR176"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 07 00 00 00 00 ..",
			"MIFARE Plus 4K SL1", "SRI X4K"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 0A 00 00 00 00 ..",
			"MIFARE Plus 2K SL2", "AT88SC0808CRF"},
		{"3B 8F 80 01 80 4F 0C A0 00 00 03 06 .. 00 0B 00 00 00 00 ..",
			"MIFARE Plus 4K SL2", "AT88SC1616CRF"},
	}

	for _, tt := range tests {
		t.Run(tt.is, func(t *testing.T) {
			// None of these has a driver here, so the honest answer is to not
			// recognise them and let command detection have its turn.
			if got := DetectTagTypeFromATR(atrFromRegistry(t, tt.atr)); got != DetectedUnknown {
				t.Errorf("DetectTagTypeFromATR = %v, want DetectedUnknown; this name was read as %q and the registry calls it %q",
					got, tt.was, tt.is)
			}
		})
	}
}

// The standard byte varies by reader, so the same card name is the same card
// whatever precedes it. The registry wildcards that byte for exactly this
// reason.
func TestATRIgnoresTheStandardByte(t *testing.T) {
	for _, standard := range []string{"00", "03", "11", "FF"} {
		atr := "3B 8F 80 01 80 4F 0C A0 00 00 03 06 " + standard + " 00 3B 00 00 00 00 42"
		if got := DetectTagTypeFromATR(atrFromRegistry(t, atr)); got != DetectedFeliCa {
			t.Errorf("standard byte %s: got %v, want DetectedFeliCa", standard, got)
		}
	}
}
