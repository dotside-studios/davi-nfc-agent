package remotenfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// A QR/barcode card is a URL printed on paper rather than written to a chip. A
// camera decodes it and reports the value as a uri record, exactly as a phone
// reports the NDEF off an NFC tag. The bridge stays incurious: it carries the
// scan through without judging what the code is, and a consumer reads the URL
// and keys on it the same way it does for an NFC tap.

// qrScan is a scan as a camera device would report a QR card: a non-hex UID and
// the card URL as a uri record.
func qrScan() TagData {
	return TagData{
		DeviceID:   "dev_cam",
		UID:        "https://davi.social/c/QR-ABC123",
		Technology: "qr",
		Type:       "qr_card",
		NDEFMessage: &protocol.NDEFMessageInput{
			Records: []protocol.NDEFRecordInput{
				{RecordType: "uri", Content: "https://davi.social/c/QR-ABC123"},
			},
		},
	}
}

func TestNonNFCUIDCarriedVerbatim(t *testing.T) {
	tag, err := convertTagData(qrScan(), nil)
	if err != nil {
		t.Fatalf("convertTagData(non-NFC) error = %v", err)
	}
	// The non-hex identifier reaches a consumer byte-for-byte: the bridge does
	// not colon-format or upper-case it the way it normalizes an NFC serial.
	if got, want := tag.UID(), "https://davi.social/c/QR-ABC123"; got != want {
		t.Errorf("UID() = %q, want verbatim %q", got, want)
	}
}

func TestHexUIDStillNormalized(t *testing.T) {
	// The reframe must not loosen the NFC path: a hex serial is still canonicalized
	// so a device and a client agree on it for write targeting.
	scan := qrScan()
	scan.UID = "04abcdef"
	scan.Technology = "ISO14443A"
	scan.NDEFMessage = nil

	tag, err := convertTagData(scan, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got, want := tag.UID(), "04:AB:CD:EF"; got != want {
		t.Errorf("UID() = %q, want normalized %q", got, want)
	}
}

func TestNonNFCScanIsReadOnlyByNature(t *testing.T) {
	// No special-casing makes a QR read-only: a camera declares it cannot write,
	// and the existing capability machinery refuses the operation. Prove it with
	// a device that declares read-only, the way a camera does.
	tag, err := convertTagData(qrScan(), capableDevice{write: false, lock: false, transceive: false})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	caps := tag.Capabilities()
	if !caps.CanRead {
		t.Error("CanRead = false, want true")
	}
	if caps.CanWrite {
		t.Error("CanWrite = true for a camera-declared read-only scan, want false")
	}
	if caps.CanLock {
		t.Error("CanLock = true, want false")
	}
	if caps.CanTransceive {
		t.Error("CanTransceive = true, want false")
	}

	if _, ok := tag.(*Tag); !ok {
		t.Fatalf("tag is %T, want *Tag", tag)
	}
	if err := tag.WriteData([]byte{0x01}); err == nil {
		t.Error("WriteData succeeded on a read-only scan, want NotSupported")
	}
	_ = nfc.Tag(tag)
}
