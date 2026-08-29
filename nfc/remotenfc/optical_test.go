package remotenfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// uriScan builds a QR-style optical scan carrying a URL as a uri record, the
// shape a camera device reports for a QR card.
func uriScan(uid, url string) TagData {
	return TagData{
		DeviceID: "dev_cam",
		UID:      uid,
		Format:   "qr",
		NDEFMessage: &protocol.NDEFMessageInput{
			Records: []protocol.NDEFRecordInput{
				{RecordType: "uri", Content: url},
			},
		},
	}
}

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"qr":       "qr",
		"QR":       "qr",
		"QR-Code":  "qr",
		"qr_code":  "qr",
		"qrcode":   "qr",
		"EAN-13":   "ean13",
		"Code 128": "code128",
	}
	for in, want := range cases {
		if got := normalizeFormat(in); got != want {
			t.Errorf("normalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConvertOpticalTagNoHexUID(t *testing.T) {
	// A QR carries no hex serial. Conversion must not reject it the way it
	// rejects a malformed NFC UID.
	tag, err := convertTagData(uriScan("", "https://davi.social/u/abc"), nil)
	if err != nil {
		t.Fatalf("convertTagData(optical) error = %v", err)
	}

	rt, ok := tag.(*Tag)
	if !ok {
		t.Fatalf("tag is %T, want *Tag", tag)
	}
	if rt.Format() != "qr" {
		t.Errorf("Format() = %q, want %q", rt.Format(), "qr")
	}
	if got := tag.UID(); got == "" || got[:len(opticalUIDPrefix)] != opticalUIDPrefix {
		t.Errorf("derived UID = %q, want a %q-prefixed identity", got, opticalUIDPrefix)
	}
	if tag.Type() != "QR Code" {
		t.Errorf("Type() = %q, want %q", tag.Type(), "QR Code")
	}
}

func TestConvertOpticalDerivedUIDIsStable(t *testing.T) {
	// The same code must resolve to the same tag on every scan: identity is the
	// credential, not the individual card.
	a, err := convertTagData(uriScan("", "https://davi.social/u/abc"), nil)
	if err != nil {
		t.Fatalf("convert a: %v", err)
	}
	b, err := convertTagData(uriScan("", "https://davi.social/u/abc"), nil)
	if err != nil {
		t.Fatalf("convert b: %v", err)
	}
	if a.UID() != b.UID() {
		t.Errorf("same content produced different UIDs: %q vs %q", a.UID(), b.UID())
	}

	c, err := convertTagData(uriScan("", "https://davi.social/u/xyz"), nil)
	if err != nil {
		t.Fatalf("convert c: %v", err)
	}
	if a.UID() == c.UID() {
		t.Errorf("different content produced the same UID: %q", a.UID())
	}
}

func TestConvertOpticalHonorsProvidedUID(t *testing.T) {
	// A device may set its own identity; the bridge must not overwrite it with a
	// derived hash, and must not force it through hex normalization.
	tag, err := convertTagData(uriScan("member-42", "https://davi.social/u/abc"), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if tag.UID() != "member-42" {
		t.Errorf("UID() = %q, want provided %q", tag.UID(), "member-42")
	}
}

func TestConvertOpticalNoContentRejected(t *testing.T) {
	// An optical scan with neither a UID nor any content has no identity.
	_, err := convertTagData(TagData{DeviceID: "dev_cam", Format: "qr"}, nil)
	if err == nil {
		t.Fatal("expected error for optical scan with no UID and no content")
	}
}

func TestOpticalTagIsReadOnly(t *testing.T) {
	// Even when the device claims it can write and lock, an optical code cannot:
	// the bridge forces those off at the tag rather than trusting the device.
	scan := uriScan("", "https://davi.social/u/abc")
	scan.Capabilities = &protocol.TagCapabilities{
		CanRead:       true,
		CanWrite:      true,
		CanLock:       true,
		CanTransceive: true,
	}

	tag, err := convertTagData(scan, everything)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	caps := tag.Capabilities()
	if !caps.CanRead {
		t.Error("CanRead = false, want true")
	}
	if caps.CanWrite {
		t.Error("CanWrite = true for an optical code, want false")
	}
	if caps.CanLock {
		t.Error("CanLock = true for an optical code, want false")
	}
	if caps.CanTransceive {
		t.Error("CanTransceive = true for an optical code, want false")
	}
	if caps.TagFamily != "QR Code" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "QR Code")
	}

	if w, _ := tag.IsWritable(); w {
		t.Error("IsWritable() = true for an optical code, want false")
	}
	if l, _ := tag.CanMakeReadOnly(); l {
		t.Error("CanMakeReadOnly() = true for an optical code, want false")
	}
}

func TestConvertNFCTagStillRequiresHexUID(t *testing.T) {
	// The optical path must not loosen validation for NFC scans: a non-hex UID
	// with no format is still rejected.
	_, err := convertTagData(TagData{
		DeviceID:   "dev_phone",
		UID:        "not-hex",
		Technology: "ISO14443A",
	}, nil)
	if err == nil {
		t.Fatal("expected error for non-hex NFC UID")
	}
}
