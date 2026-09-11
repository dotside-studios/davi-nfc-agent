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
	DetectedDESFireEV3
	DetectedISO14443_4
	// DetectedNTAG424 is an NTAG 424 DNA, a Type 4 card with a fixed file
	// layout. Named separately from DetectedISO14443_4 so its capacity is
	// known.
	DetectedNTAG424
	DetectedPlus2K
	DetectedPlus4K
	// DetectedFeliCa is a FeliCa card. It is ISO 18092 rather than ISO 14443
	// and speaks its own command set, so the agent reads its identifier and
	// nothing else; see tag_felica.go.
	DetectedFeliCa
)

// atrCardNames maps the two-byte card name a PC/SC reader puts in the ATR's
// historical bytes onto the card it names.
//
// The name follows the standard byte, which says which contactless standard the
// card follows. The standard byte is not matched on: the ATR registry carries it
// as a wildcard for these cards, because readers differ on what they report
// there.
//
// Sourced from the registry in pcsc-tools (smartcard_list.txt), which follows
// the PC/SC Part 3 supplemental document, and pinned in TestATRCardNameVectors.
// Four of the names this table carried before did not survive that check: 0x0004
// is an SLE55R rather than a MIFARE Mini, 0x0006 and 0x0007 are ST SR176 and
// SRI X4K rather than MIFARE Plus, 0x000A and 0x000B are Atmel AT88SC parts
// rather than Plus in SL2, and 0x0026, which this table read as a DESFire, is
// the Mini. None had a driver behind it except the DESFire reading, which sent
// a Mini down the DESFire path.
var atrCardNames = map[uint16]DetectedTagType{
	0x0001: DetectedClassic1K,
	0x0002: DetectedClassic4K,
	0x0003: DetectedUltralight,
	// Not in the registry, and not contradicted by it. Kept because a driver
	// for this kind exists and nothing else produces it.
	0x0005: DetectedUltralightC,
	0x0026: DetectedMini,
	0x003B: DetectedFeliCa,
	// Observed on a DESFire EV3 4K. A DESFire that presents another name is
	// still recognised by the wrapped GET_VERSION during command detection.
	0xFF20: DetectedDESFire,
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

	// PC/SC Part 3 historical bytes: 80 4F 0C A0 00 00 03 06 SS NN NN 00 00 00 00,
	// where SS is the standard the card follows and NN NN names the card.
	for i := 0; i < len(histBytes)-10; i++ {
		// Look for standard prefix: 80 4F 0C A0 00 00 03 06
		if histBytes[i] == 0x80 && i+11 < len(histBytes) {
			if histBytes[i+1] == 0x4F &&
				histBytes[i+3] == 0xA0 &&
				histBytes[i+4] == 0x00 &&
				histBytes[i+5] == 0x00 &&
				histBytes[i+6] == 0x03 &&
				histBytes[i+7] == 0x06 {
				name := uint16(histBytes[i+9])<<8 | uint16(histBytes[i+10])
				if t, ok := atrCardNames[name]; ok {
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

// Version fields the two GET_VERSION parsers below read, as published in the
// NXP product data sheets.
const (
	versionVendorNXP = 0x04

	// Product types, byte 2 of a version response.
	versionProductUltralight = 0x03
	versionProductNTAG       = 0x04

	// Storage sizes, byte 6. The same value can mean two different cards across
	// families, so it does not identify one on its own.
	versionStorage128B = 0x0E // MF0UL21
	versionStorage144B = 0x0F // NTAG213
	versionStorage504B = 0x11 // NTAG215, and the NTAG 424 DNA
	versionStorage888B = 0x13 // NTAG216

	// Protocol types, byte 7. NTAG21x are ISO 14443-3 and addressed by page;
	// the NTAG 424 DNA is ISO 14443-4 and addressed by file.
	versionProtocol14443_3 = 0x03
	versionProtocol14443_4 = 0x05

	// versionMajorNTAG424 is the NTAG 424 DNA's major version. Pinned so a
	// later ISO 14443-4 NTAG is not given this card's memory layout; it stays a
	// generic Type 4 tag instead.
	versionMajorNTAG424 = 0x30

	// versionProductDESFire is the product type every DESFire reports.
	versionProductDESFire = 0x01

	// DESFire hardware major versions, byte 4 of a version response. A major
	// this does not name is driven as a plain DESFire, which is what the
	// generations have in common.
	versionMajorDESFireEV1 = 0x01
	versionMajorDESFireEV2 = 0x12
	versionMajorDESFireEV3 = 0x33
)

// WrappedVersion is the hardware half of a GET_VERSION answered over
// ISO 14443-4, as an NTAG 424 DNA or a DESFire answers 90 60 00 00 00. A full
// version spans three frames; this is the first, which carries the fields that
// identify the card.
type WrappedVersion struct {
	Vendor       byte
	ProductType  byte
	Subtype      byte
	MajorVersion byte
	MinorVersion byte
	StorageSize  byte
	Protocol     byte
}

// Kind names the card, or DetectedUnknown for one this does not recognise.
func (v WrappedVersion) Kind() DetectedTagType {
	if v.Vendor != versionVendorNXP {
		return DetectedUnknown
	}

	switch v.ProductType {
	case versionProductNTAG:
		if v.Protocol == versionProtocol14443_4 &&
			v.StorageSize == versionStorage504B &&
			v.MajorVersion == versionMajorNTAG424 {
			return DetectedNTAG424
		}
		return DetectedUnknown

	case versionProductDESFire:
		switch v.MajorVersion {
		case versionMajorDESFireEV1:
			return DetectedDESFireEV1
		case versionMajorDESFireEV2:
			return DetectedDESFireEV2
		case versionMajorDESFireEV3:
			return DetectedDESFireEV3
		}
		return DetectedDESFire

	default:
		return DetectedUnknown
	}
}

// MemorySize reports the EEPROM size the storage byte encodes, or 0 when it
// encodes none. The byte is 2^(n>>1) bytes; an odd byte means the size falls
// between that and the next power of two, and the lower bound is reported.
//
// This is the DESFire encoding. It does not describe an NTAG 424 DNA, whose
// usable memory is the sum of three fixed files rather than its EEPROM.
func (v WrappedVersion) MemorySize() int {
	exp := int(v.StorageSize >> 1)
	if exp < 1 || exp > 30 {
		return 0
	}
	return 1 << exp
}

// ParseWrappedVersion reads the first frame of a GET_VERSION answered over
// ISO 14443-4.
//
// The encoding differs from the native one in two ways: the frame carries no
// leading header byte, so every field sits one earlier, and the status is in
// SW2 under SW1=0x91, where 0xAF means more frames follow.
//
// The second result is false for a response this cannot read, including one
// from a card that does not implement the command.
func ParseWrappedVersion(raw []byte) (WrappedVersion, bool) {
	parsed, err := ParseAPDUResponse(raw)
	if err != nil {
		return WrappedVersion{}, false
	}

	// 91 AF is the expected answer. 91 00 covers a card that fits its version
	// in one frame, 90 00 a reader that unwraps the status itself.
	wrapped := parsed.SW1 == 0x91 && (parsed.SW2 == dfStatusAdditionalFrame || parsed.SW2 == dfStatusOK)
	if !wrapped && !parsed.IsSuccess() {
		return WrappedVersion{}, false
	}

	// Vendor, product type, subtype, major, minor, storage, protocol.
	const wrappedVersionLen = 7
	if len(parsed.Data) < wrappedVersionLen {
		return WrappedVersion{}, false
	}
	return WrappedVersion{
		Vendor:       parsed.Data[0],
		ProductType:  parsed.Data[1],
		Subtype:      parsed.Data[2],
		MajorVersion: parsed.Data[3],
		MinorVersion: parsed.Data[4],
		StorageSize:  parsed.Data[5],
		Protocol:     parsed.Data[6],
	}, true
}

// ParseGetVersionResponse parses GET_VERSION response to determine tag type.
//
// Only the EV1 generations answer GET_VERSION at all: the original Ultralight
// and the Ultralight C do not implement the command, so a reply carrying the
// Ultralight product type identifies an Ultralight EV1 rather than either of
// those.
//
// This is the native encoding. For the ISO 14443-4 form, see
// ParseWrappedGetVersionResponse.
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
		// The protocol byte is read before the storage size, because an
		// NTAG 424 DNA reports the same 0x11 as an NTAG215. Keying on size
		// alone would drive a file-based card with the page-addressed driver
		// below, which fails on the first read.
		if protocolType == versionProtocol14443_4 {
			if storageSize == versionStorage504B {
				return DetectedNTAG424
			}
			// Some other ISO 14443-4 NTAG, whose layout is unknown here. The
			// caller falls back to the generic Type 4 driver.
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

// ParseWrappedGetVersionResponse names the card behind a GET_VERSION answered
// over ISO 14443-4. See ParseWrappedVersion for the fields it reads.
func ParseWrappedGetVersionResponse(raw []byte) (DetectedTagType, bool) {
	version, ok := ParseWrappedVersion(raw)
	if !ok {
		return DetectedUnknown, false
	}
	kind := version.Kind()
	return kind, kind != DetectedUnknown
}
