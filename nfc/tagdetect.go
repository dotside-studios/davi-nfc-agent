package nfc

// DetectedTagType represents detected tag type from ATR/commands
type DetectedTagType int

// Detected tag type constants for PC/SC detection
const (
	DetectedUnknown DetectedTagType = iota
	DetectedClassic1K
	DetectedClassic4K
	DetectedMini
	DetectedUltralight
	DetectedUltralightC
	// DetectedUltralightEV1 is an MF0UL11: 48 bytes of user memory, laid out
	// like the original Ultralight.
	DetectedUltralightEV1
	// DetectedUltralightEV1_128 is an MF0UL21: 128 bytes of user memory, which
	// reaches past the pages the static lock bytes cover.
	DetectedUltralightEV1_128
	DetectedNTAG213
	DetectedNTAG215
	DetectedNTAG216
	DetectedDESFire
	DetectedDESFireEV1
	DetectedDESFireEV2
	DetectedISO14443_4
	// DetectedNTAG424 is an NTAG 424 DNA: a Type 4 card whose NDEF file is
	// reached with the same ISO 7816 commands as any other, but whose layout
	// and AES-protected files are its own. Named separately so its capacity is
	// known rather than unbounded.
	DetectedNTAG424
	DetectedPlus2K
	DetectedPlus4K
)

// ATR historical byte patterns for tag type detection
// These are found in the ATR returned by PC/SC readers
var atrPatterns = map[byte]DetectedTagType{
	0x01: DetectedClassic1K,
	0x02: DetectedClassic4K,
	0x03: DetectedUltralight,
	0x04: DetectedMini,
	0x05: DetectedUltralightC,
	0x06: DetectedPlus2K,  // MIFARE Plus 2K in SL1
	0x07: DetectedPlus4K,  // MIFARE Plus 4K in SL1
	0x0A: DetectedPlus2K,  // MIFARE Plus 2K in SL2
	0x0B: DetectedPlus4K,  // MIFARE Plus 4K in SL2
	0x26: DetectedDESFire, // DESFire (various versions)
}

// DetectTagTypeFromATR parses ATR and returns detected tag type
func DetectTagTypeFromATR(atr []byte) DetectedTagType {
	if len(atr) < 2 {
		return DetectedUnknown
	}

	// Common ATR formats for contactless cards:
	// 3B 8F 80 01 80 4F 0C A0 00 00 03 06 03 00 XX 00 00 00 00 YY
	//                                        ^^ Card type byte
	//
	// Look for the card type in historical bytes

	// Find historical bytes (after 3B and interface bytes)
	histStart := findHistoricalBytesStart(atr)
	if histStart < 0 || histStart >= len(atr) {
		return DetectedUnknown
	}

	histBytes := atr[histStart:]

	// Look for PC/SC 2.01 Part 3 format
	// Historical bytes: 80 4F 0C A0 00 00 03 06 03 00 XX ...
	// Where XX is the card type
	for i := 0; i < len(histBytes)-10; i++ {
		// Look for standard prefix: 80 4F 0C A0 00 00 03 06
		if histBytes[i] == 0x80 && i+11 < len(histBytes) {
			if histBytes[i+1] == 0x4F &&
				histBytes[i+3] == 0xA0 &&
				histBytes[i+4] == 0x00 &&
				histBytes[i+5] == 0x00 &&
				histBytes[i+6] == 0x03 &&
				histBytes[i+7] == 0x06 {
				// Found the pattern, card type is at offset +10
				cardType := histBytes[i+10]
				if t, ok := atrPatterns[cardType]; ok {
					return t
				}
			}
		}
	}

	// Fallback: check for ISO14443-4 compliance in ATR
	// This is indicated by certain byte patterns
	if containsISO14443_4Indicator(atr) {
		return DetectedISO14443_4
	}

	return DetectedUnknown
}

// findHistoricalBytesStart finds the start of historical bytes in ATR
func findHistoricalBytesStart(atr []byte) int {
	if len(atr) < 2 {
		return -1
	}

	// ATR format:
	// TS (3B or 3F)
	// T0 (format byte, lower nibble = number of historical bytes)
	// TA1, TB1, TC1, TD1 (optional, indicated by T0)
	// TA2, TB2, TC2, TD2 (optional, indicated by TD1)
	// ... more interface bytes
	// Historical bytes
	// TCK (check byte, only if T!=0)

	ts := atr[0]
	if ts != 0x3B && ts != 0x3F {
		return -1
	}

	t0 := atr[1]
	numHistBytes := int(t0 & 0x0F)
	if numHistBytes == 0 {
		return -1
	}

	// Count interface bytes
	pos := 2
	td := t0

	for {
		if (td & 0x10) != 0 {
			pos++ // TAi present
		}
		if (td & 0x20) != 0 {
			pos++ // TBi present
		}
		if (td & 0x40) != 0 {
			pos++ // TCi present
		}
		if (td & 0x80) != 0 {
			if pos >= len(atr) {
				return -1
			}
			td = atr[pos] // TDi present, read it
			pos++
		} else {
			break
		}
	}

	if pos >= len(atr) {
		return -1
	}

	return pos
}

// containsISO14443_4Indicator checks for ISO14443-4 indicators in ATR
func containsISO14443_4Indicator(atr []byte) bool {
	// Look for TD1 byte with T=1 protocol (ISO14443-4 uses T=CL which maps to T=1)
	if len(atr) < 3 {
		return false
	}

	t0 := atr[1]
	pos := 2

	// Skip TA1, TB1, TC1
	if (t0 & 0x10) != 0 {
		pos++
	}
	if (t0 & 0x20) != 0 {
		pos++
	}
	if (t0 & 0x40) != 0 {
		pos++
	}

	// Check TD1
	if (t0&0x80) != 0 && pos < len(atr) {
		td1 := atr[pos]
		// Lower nibble is protocol type (T value)
		// T=1 indicates potential ISO14443-4 support
		if (td1 & 0x0F) == 0x01 {
			return true
		}
	}

	return false
}

// Version bytes shared by the two GET_VERSION encodings below. NXP publishes
// these in the product data sheets; they are the only fields either parser
// needs to name a card.
const (
	// versionVendorNXP is the vendor ID every card here carries.
	versionVendorNXP = 0x04

	// Product types, byte 2 of a version response.
	versionProductUltralight = 0x03
	versionProductNTAG       = 0x04

	// Storage sizes, byte 6. The value encodes the user memory, so it separates
	// the members of a family — except across families, where the same size can
	// mean two different cards and the protocol byte has to decide.
	versionStorage128B = 0x0E // MF0UL21
	versionStorage144B = 0x0F // NTAG213
	versionStorage504B = 0x11 // NTAG215, and the NTAG 424 DNA
	versionStorage888B = 0x13 // NTAG216

	// Protocol types, byte 7. The page-addressed NTAG21x speak ISO 14443-3
	// only; the NTAG 424 DNA is ISO 14443-4, which is what makes it a Type 4
	// card driven by SELECT and READ BINARY rather than by page.
	versionProtocol14443_3 = 0x03
	versionProtocol14443_4 = 0x05

	// versionMajorNTAG424 is the NTAG 424 DNA's major version. Identifying a
	// card is a claim about its capacity, so the generation is pinned: a later
	// ISO 14443-4 NTAG reads as a plain Type 4 tag, which still works, rather
	// than borrowing this one's memory layout.
	versionMajorNTAG424 = 0x30
)

// ParseGetVersionResponse parses GET_VERSION response to determine tag type.
//
// Only the EV1 generations answer GET_VERSION at all: the original Ultralight
// and the Ultralight C do not implement the command, so a reply carrying the
// Ultralight product type identifies an Ultralight EV1 rather than either of
// those.
//
// This is the native encoding, as an ISO 14443-3 card answers it. A card driven
// over ISO 14443-4 answers the wrapped form instead, which omits the header
// byte and carries its status in SW2; see ParseWrappedGetVersionResponse.
//
// Response format for NTAG/Ultralight EV1:
// Byte 0: Fixed header 0x00
// Byte 1: Vendor ID (0x04 = NXP)
// Byte 2: Product type (0x03 = Ultralight, 0x04 = NTAG)
// Byte 3: Product subtype
// Byte 4: Major version
// Byte 5: Minor version
// Byte 6: Storage size
// Byte 7: Protocol type
func ParseGetVersionResponse(resp []byte) DetectedTagType {
	if len(resp) < 8 {
		return DetectedUnknown
	}

	vendorID := resp[1]
	productType := resp[2]
	storageSize := resp[6]
	protocolType := resp[7]

	// NXP vendor
	if vendorID != versionVendorNXP {
		return DetectedUnknown
	}

	switch productType {
	case versionProductUltralight: // Ultralight EV1 family
		switch storageSize {
		case versionStorage128B: // 128 bytes of user memory (MF0UL21)
			return DetectedUltralightEV1_128
		default: // 48 bytes (MF0UL11); read an unknown variant conservatively
			return DetectedUltralightEV1
		}

	case versionProductNTAG:
		// The NTAG product type spans two incompatible families. The protocol
		// byte separates them, and it has to be read before the storage size:
		// an NTAG 424 DNA reports the same 0x11 as an NTAG215, so keying on
		// size alone would drive a file-based card with a page-addressed
		// driver, which fails on the first read.
		if protocolType == versionProtocol14443_4 {
			if storageSize == versionStorage504B {
				return DetectedNTAG424
			}
			// Another ISO 14443-4 NTAG. Nothing here knows its layout, and
			// guessing a page-addressed one would be wrong; the caller falls
			// back to the generic Type 4 driver.
			return DetectedUnknown
		}

		switch storageSize {
		case versionStorage144B:
			return DetectedNTAG213
		case versionStorage504B:
			return DetectedNTAG215
		case versionStorage888B:
			return DetectedNTAG216
		}
		return DetectedNTAG215 // Default NTAG

	default:
		return DetectedUnknown
	}
}

// ParseWrappedGetVersionResponse identifies a card from the first frame of a
// GET_VERSION answered over ISO 14443-4, as an NTAG 424 DNA or a DESFire
// answers the ISO-wrapped command 90 60 00 00 00.
//
// Two things differ from the native encoding. The frame carries no leading
// header byte, so every field sits one earlier; and the card reports its status
// in SW2 under SW1=0x91, answering 0xAF — more frames follow — for the first of
// the three frames a full version spans. Only the first frame is read here,
// because it holds everything that names the card.
//
// The second result is false for anything this cannot name, including a card
// that does not implement the command at all: a Type 4 tag that answers "class
// not supported" is simply one whose kind stays generic.
func ParseWrappedGetVersionResponse(raw []byte) (DetectedTagType, bool) {
	parsed, err := ParseAPDUResponse(raw)
	if err != nil {
		return DetectedUnknown, false
	}

	// 91 AF is the expected answer, with 91 00 accepted for a card that fits
	// its version in one frame and 90 00 for a reader that unwraps the status
	// itself.
	wrapped := parsed.SW1 == 0x91 && (parsed.SW2 == dfStatusAdditionalFrame || parsed.SW2 == dfStatusOK)
	if !wrapped && !parsed.IsSuccess() {
		return DetectedUnknown, false
	}

	// Vendor, product type, subtype, major, minor, storage, protocol.
	const wrappedVersionLen = 7
	if len(parsed.Data) < wrappedVersionLen {
		return DetectedUnknown, false
	}
	vendorID := parsed.Data[0]
	productType := parsed.Data[1]
	majorVersion := parsed.Data[3]
	storageSize := parsed.Data[5]
	protocolType := parsed.Data[6]

	if vendorID != versionVendorNXP {
		return DetectedUnknown, false
	}
	if productType == versionProductNTAG &&
		protocolType == versionProtocol14443_4 &&
		storageSize == versionStorage504B &&
		majorVersion == versionMajorNTAG424 {
		return DetectedNTAG424, true
	}
	return DetectedUnknown, false
}
