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
			if got := detectTagTypeFromATR(atrWithCardType(tt.cardType)); got != tt.want {
				t.Errorf("detectTagTypeFromATR(%s) = %v, want %v", tt.name, got, tt.want)
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
			if got := detectTagTypeFromATR(atr); got != DetectedUnknown {
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
		{"Ultralight", mk(0x03, 0x0B), DetectedUltralight},
		{"Ultralight C", mk(0x03, 0x0E), DetectedUltralightC},
		{"Ultralight EV1", mk(0x03, 0x0F), DetectedUltralightEV1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseGetVersionResponse(tt.resp); got != tt.want {
				t.Errorf("parseGetVersionResponse(%s) = %v, want %v", tt.name, got, tt.want)
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
			if got := parseGetVersionResponse(resp); got != DetectedUnknown {
				t.Errorf("expected DetectedUnknown for %s, got %v", name, got)
			}
		})
	}
}
