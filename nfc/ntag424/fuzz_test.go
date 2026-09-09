package ntag424

import (
	"errors"
	"strings"
	"testing"
)

// Verification reads whatever a caller was handed: a URL from a phone, a
// scanner, or anyone who guessed the endpoint. It must answer any input without
// panicking, and it must never report a tap it did not verify.
func FuzzVerifyURL(f *testing.F) {
	f.Add("https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337086")
	f.Add("https://my424dna.com/?picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842B&cmac=C48B89C17A233B2C")
	f.Add("https://sdm.nfcdeveloper.com/tagpt?uid=041E3C8A2D6B80&ctr=000006&cmac=4B00064004B0B3D3")
	f.Add("https://example.com/?cmac=")
	f.Add("?e=&c=&enc=")
	f.Add("%%")
	f.Add("")

	f.Fuzz(func(t *testing.T, rawURL string) {
		tap, err := VerifyURL(rawURL, zeroKeys)
		if err != nil {
			if tap != nil {
				t.Errorf("a failed verification returned a tap: %+v", tap)
			}
			return
		}

		// A verified tap is one this package would verify again, and its UID is
		// either absent or the one length a tag has.
		if len(tap.UID) != 0 && len(tap.UID) != uidLength {
			t.Errorf("verified a tap with a %d-byte UID", len(tap.UID))
		}
		if _, err := VerifyURL(rawURL, zeroKeys); err != nil {
			t.Errorf("verification is not repeatable: %v", err)
		}
	})
}

// The primitives take attacker-chosen lengths too, through any caller that
// parses a URL itself.
func FuzzVerifyMAC(f *testing.F) {
	f.Add(
		[]byte{0xEF, 0x96, 0x3F, 0xF7, 0x82, 0x86, 0x58, 0xA5, 0x99, 0xF3, 0x04, 0x15, 0x10, 0x67, 0x1E, 0x88},
		[]byte{},
		[]byte{0x94, 0xEE, 0xD9, 0xEE, 0x65, 0x33, 0x70, 0x86},
	)
	f.Add([]byte{}, []byte{}, []byte{})

	f.Fuzz(func(t *testing.T, encrypted, input, mac []byte) {
		data, err := DecryptPICCData(zeroKey, encrypted)
		if err != nil {
			if !errors.Is(err, ErrPICCData) {
				t.Errorf("DecryptPICCData failed with %v, want ErrPICCData", err)
			}
			return
		}
		if err := VerifyMAC(zeroKey, data, input, mac); err != nil && !errors.Is(err, ErrMACMismatch) {
			t.Errorf("VerifyMAC failed with %v, want ErrMACMismatch", err)
		}
	})
}

// A MAC parameter long enough to matter must not be read as a valid one. The
// URL is text, so its length is the caller's, not the tag's.
func TestVerifyURLHandlesAnOverlongParameter(t *testing.T) {
	raw := "https://example.com/?picc_data=EF963FF7828658A599F3041510671E88&cmac=" + strings.Repeat("AB", 4096)
	if _, err := VerifyURL(raw, zeroKeys); err == nil {
		t.Error("a 4096-byte MAC was accepted")
	}
}
