package nfc

import "testing"

// addParserSeeds seeds the parser fuzz targets with valid, edge, and malformed
// inputs: empty, bare type/terminator bytes, a well-formed NDEF record, a
// TLV-wrapped NDEF message, a long-format length with a truncated value, and a
// lock-control TLV preceding an NDEF TLV.
func addParserSeeds(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x00},
		{0xFE},
		{0x03},
		{0x03, 0x00, 0xFE},
		{0xD1, 0x01, 0x04, 0x54, 0x02, 0x65, 0x6E, 0x48, 0x69},
		{0x03, 0x09, 0xD1, 0x01, 0x04, 0x54, 0x02, 0x65, 0x6E, 0x48, 0x69, 0xFE},
		{0x03, 0xFF, 0x01, 0x00},                                     // long-format length, value truncated
		{0x01, 0x03, 0xAA, 0xBB, 0xCC, 0x03, 0x02, 0xD0, 0x00, 0xFE}, // lock-ctrl then NDEF
	}
	for _, s := range seeds {
		f.Add(s)
	}
}

// FuzzDecodeNDEF ensures DecodeNDEF never panics on arbitrary input, and that a
// successful decode round-trips through Encode without panicking. Runs its seed
// corpus on every `go test`; use `go test -fuzz=FuzzDecodeNDEF` for active
// fuzzing.
func FuzzDecodeNDEF(f *testing.F) {
	addParserSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := DecodeNDEF(data)
		if err != nil {
			return
		}
		_, _ = msg.Encode()
	})
}

// FuzzTLVParsers ensures the TLV parsers never panic or over-read on arbitrary
// input (truncated lengths, long-format edges, missing terminators).
func FuzzTLVParsers(f *testing.F) {
	addParserSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = TLVFindNDEF(data)
		_, _ = TLVDecode(data)
		_ = ParseTLVBlock(data)
	})
}

// FuzzParseAPDUResponse ensures parsing of arbitrary reader responses never
// panics (short responses, missing status words, etc.).
func FuzzParseAPDUResponse(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x90, 0x00},
		{0x63, 0x00},
		{0x6A, 0x82},
		{0x01, 0x02, 0x90, 0x00},
		{0xFF},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseAPDUResponse(data)
	})
}
