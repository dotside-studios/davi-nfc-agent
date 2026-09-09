package nfc

import "testing"

// atrWithCardType builds a PC/SC ATR whose historical bytes carry the standard
// MIFARE pattern (80 4F 0C A0 00 00 03 06 03 00 XX), with XX the card-type byte.
func atrWithCardType(cardType byte) []byte {
	return []byte{
		0x3B, 0x8F, 0x80, 0x01, // TS, T0, TD1, TD2
		0x80, 0x4F, 0x0C, 0xA0, 0x00, 0x00, 0x03, 0x06, 0x03, 0x00, cardType,
		0x00, 0x00, 0x00, 0x00, // RFU
		0x00, // TCK (value irrelevant to the parser)
	}
}

func TestDetectTagTypeFromATR(t *testing.T) {
	tests := []struct {
		name     string
		cardType byte
		want     DetectedTagType
	}{
		{"Classic 1K", 0x01, DetectedClassic1K},
		{"Classic 4K", 0x02, DetectedClassic4K},
		{"Ultralight", 0x03, DetectedUltralight},
		{"Mini", 0x04, DetectedMini},
		{"Ultralight C", 0x05, DetectedUltralightC},
		{"DESFire", 0x26, DetectedDESFire},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectTagTypeFromATR(atrWithCardType(tt.cardType)); got != tt.want {
				t.Errorf("DetectTagTypeFromATR(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectTagTypeFromATR_Unrecognized(t *testing.T) {
	cases := map[string][]byte{
		"too short":    {0x3B},
		"no pattern":   {0x3B, 0x82, 0x12, 0x34, 0x56},
		"unknown type": atrWithCardType(0x99),
		"empty":        {},
		"bad TS":       {0x00, 0x8F, 0x80, 0x01, 0x80, 0x4F, 0x0C, 0xA0, 0x00, 0x00, 0x03, 0x06, 0x03, 0x00, 0x01},
	}
	for name, atr := range cases {
		t.Run(name, func(t *testing.T) {
			if got := DetectTagTypeFromATR(atr); got != DetectedUnknown {
				t.Errorf("expected DetectedUnknown for %s, got %v", name, got)
			}
		})
	}
}

func TestParseGetVersionResponse(t *testing.T) {
	// resp: [header, vendor, product, subtype, major, minor, storage, protocol]
	mk := func(product, storage byte) []byte {
		return []byte{0x00, 0x04, product, 0x01, 0x01, 0x00, storage, 0x03}
	}
	tests := []struct {
		name string
		resp []byte
		want DetectedTagType
	}{
		{"NTAG213", mk(0x04, 0x0F), DetectedNTAG213},
		{"NTAG215", mk(0x04, 0x11), DetectedNTAG215},
		{"NTAG216", mk(0x04, 0x13), DetectedNTAG216},
		// The original Ultralight and the Ultralight C do not implement
		// GET_VERSION, so an Ultralight product type in a reply is always an
		// EV1; the storage size then says which one.
		{"Ultralight EV1 48-byte", mk(0x03, 0x0B), DetectedUltralightEV1},
		{"Ultralight EV1 128-byte", mk(0x03, 0x0E), DetectedUltralightEV1_128},
		{"Ultralight EV1 unknown size", mk(0x03, 0x0F), DetectedUltralightEV1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseGetVersionResponse(tt.resp); got != tt.want {
				t.Errorf("ParseGetVersionResponse(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseGetVersionResponse_Invalid(t *testing.T) {
	cases := map[string][]byte{
		"short":          {0x00, 0x04, 0x04},
		"non-NXP vendor": {0x00, 0x05, 0x04, 0x01, 0x01, 0x00, 0x0F, 0x03},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseGetVersionResponse(resp); got != DetectedUnknown {
				t.Errorf("expected DetectedUnknown for %s, got %v", name, got)
			}
		})
	}
}

// The NTAG family spans two incompatible protocols, and the storage size does
// not separate them: an NTAG 424 DNA reports the same 0x11 as an NTAG215. The
// protocol byte is what decides, and getting it wrong drives a file-based card
// with the page-addressed NTAG driver, which fails on the first read.
func TestParseGetVersionResponse_ProtocolSeparatesTheNTAGFamilies(t *testing.T) {
	// resp: [header, vendor, product, subtype, major, minor, storage, protocol]
	mk := func(major, storage, protocol byte) []byte {
		return []byte{0x00, 0x04, 0x04, 0x02, major, 0x00, storage, protocol}
	}

	tests := []struct {
		name string
		resp []byte
		want DetectedTagType
	}{
		{"NTAG215 keeps its 0x11 under ISO 14443-3", mk(0x01, 0x11, 0x03), DetectedNTAG215},
		{"NTAG 424 DNA is the same 0x11 under ISO 14443-4", mk(0x30, 0x11, 0x05), DetectedNTAG424},
		// An unrecognised size under the page-addressed protocol still reads as
		// an NTAG21x, which is the existing conservative default.
		{"unknown size, ISO 14443-3", mk(0x01, 0x77, 0x03), DetectedNTAG215},
		// Under ISO 14443-4 there is no such default to fall back on: nothing
		// here knows the layout, so the caller is left to drive it as a plain
		// Type 4 card rather than by page.
		{"unknown size, ISO 14443-4", mk(0x30, 0x77, 0x05), DetectedUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseGetVersionResponse(tt.resp); got != tt.want {
				t.Errorf("ParseGetVersionResponse = %v, want %v", got, tt.want)
			}
		})
	}
}

// The wrapped form omits the header byte the native one carries and reports its
// status in SW2, so it is parsed separately. Reading it with the native parser's
// offsets would shift every field by one.
func TestParseWrappedGetVersionResponse(t *testing.T) {
	// frame: [vendor, product, subtype, major, minor, storage, protocol]
	ntag424 := []byte{0x04, 0x04, 0x02, 0x30, 0x00, 0x11, 0x05}
	withSW := func(frame []byte, sw1, sw2 byte) []byte {
		return append(append([]byte(nil), frame...), sw1, sw2)
	}

	t.Run("first frame of the chain", func(t *testing.T) {
		kind, ok := ParseWrappedGetVersionResponse(withSW(ntag424, 0x91, 0xAF))
		if !ok || kind != DetectedNTAG424 {
			t.Errorf("got (%v, %v), want (DetectedNTAG424, true)", kind, ok)
		}
	})

	t.Run("a reader that unwraps the status", func(t *testing.T) {
		kind, ok := ParseWrappedGetVersionResponse(withSW(ntag424, 0x90, 0x00))
		if !ok || kind != DetectedNTAG424 {
			t.Errorf("got (%v, %v), want (DetectedNTAG424, true)", kind, ok)
		}
	})

	rejected := map[string][]byte{
		"class not supported (a plain Type 4 tag)": {0x6E, 0x00},
		"too short to be a version":                withSW([]byte{0x04, 0x04}, 0x91, 0xAF),
		"non-NXP vendor":                           withSW([]byte{0x05, 0x04, 0x02, 0x30, 0x00, 0x11, 0x05}, 0x91, 0xAF),
		"page-addressed protocol":                  withSW([]byte{0x04, 0x04, 0x02, 0x01, 0x00, 0x11, 0x03}, 0x91, 0xAF),
		"a later generation this cannot size":      withSW([]byte{0x04, 0x04, 0x02, 0x40, 0x00, 0x11, 0x05}, 0x91, 0xAF),
		"DESFire":                                  withSW([]byte{0x04, 0x01, 0x01, 0x12, 0x00, 0x18, 0x05}, 0x91, 0xAF),
		"an error status":                          withSW(ntag424, 0x91, 0x1C),
	}
	for name, resp := range rejected {
		t.Run(name, func(t *testing.T) {
			if kind, ok := ParseWrappedGetVersionResponse(resp); ok {
				t.Errorf("identified %v from %s; it should not be named", kind, name)
			}
		})
	}
}
