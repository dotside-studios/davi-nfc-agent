package remotenfc

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Optical code support.
//
// A camera is just another device on the device protocol: it decodes a QR or
// barcode itself and reports the payload, exactly as a phone decodes NDEF off
// an NFC tag and reports records rather than raw RF. The agent never sees
// pixels. A scan that carries a Format is optical, and the bridge presents it
// to clients as an ordinary read-only tag: the code's content rides as an NDEF
// uri/text record so every existing client reads it through the same tagData it
// already handles.
//
// The difference an optical code cannot hide is that it is paper. It has no
// serial, and cannot be written, locked, or transceived. Its identity is the
// content printed on it, not a chip, so two cards bearing the same code are the
// same tag and a re-scan is identical. The bridge derives a stable UID from the
// content for that reason, and reports write, lock and transceive as
// unsupported however the device declared them.

// TechnologyOptical is the technology reported for a scan that came from a
// camera rather than an NFC field. It stands where "ISO14443A" stands for an
// NFC tag, so a client can tell the two apart on the same feed.
const TechnologyOptical = "optical"

// opticalUIDPrefix marks a UID the bridge derived from a code's content, to
// keep it distinct from the hex serial of an NFC tag.
const opticalUIDPrefix = "code:"

// codeFamilies maps a symbology to a display type and a tag family. The display
// type stands in for an NFC tag's model string ("NTAG215"); the family groups
// codes the way TagFamily groups chips. A symbology the registry does not name
// is still accepted; see codeDisplay.
var codeFamilies = map[string]struct{ display, family string }{
	"qr":         {"QR Code", "QR Code"},
	"datamatrix": {"Data Matrix", "2D Code"},
	"aztec":      {"Aztec", "2D Code"},
	"pdf417":     {"PDF417", "2D Code"},
	"ean13":      {"EAN-13", "Barcode"},
	"ean8":       {"EAN-8", "Barcode"},
	"upca":       {"UPC-A", "Barcode"},
	"upce":       {"UPC-E", "Barcode"},
	"code128":    {"Code 128", "Barcode"},
	"code39":     {"Code 39", "Barcode"},
	"code93":     {"Code 93", "Barcode"},
	"codabar":    {"Codabar", "Barcode"},
	"itf":        {"ITF", "Barcode"},
}

// normalizeFormat lowercases and strips separators so "QR", "qr-code", "EAN-13"
// and "ean_13" each name one symbology. It returns "" unchanged, which is what
// marks a scan as NFC rather than optical.
func normalizeFormat(format string) string {
	f := strings.ToLower(strings.TrimSpace(format))
	f = strings.NewReplacer("-", "", "_", "", " ", "").Replace(f)
	// "qrcode" and "qr" are the same symbology.
	if f == "qrcode" {
		f = "qr"
	}
	return f
}

// codeDisplay returns a display type and tag family for a symbology, falling
// back to the format itself for one the registry does not name. It accepts a
// raw or normalized format.
func codeDisplay(format string) (display, family string) {
	norm := normalizeFormat(format)
	if fam, ok := codeFamilies[norm]; ok {
		return fam.display, fam.family
	}
	if norm == "" {
		return "Optical Code", "Optical Code"
	}
	return format, "Optical Code"
}

// deriveCodeUID gives an optical scan a stable identity from the content
// printed on it, since it carries no serial. The same content always hashes to
// the same UID, which is the point: for a code, identity is the credential, not
// the individual card.
func deriveCodeUID(content []byte) string {
	sum := sha256.Sum256(content)
	return opticalUIDPrefix + hex.EncodeToString(sum[:8])
}
