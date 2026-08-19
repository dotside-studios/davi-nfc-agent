package nfc

import "testing"

// pcscTagKinds is every tag kind the reader path can build a driver for.
var pcscTagKinds = []DetectedTagType{
	DetectedClassic1K,
	DetectedClassic4K,
	DetectedUltralight,
	DetectedUltralightC,
	DetectedUltralightEV1,
	DetectedUltralightEV1_128,
	DetectedNTAG213,
	DetectedNTAG215,
	DetectedNTAG216,
	DetectedDESFire,
	DetectedISO14443_4,
}

// TestPCSCTagsSatisfyCapabilityContract runs the drivers through the same
// consistency check the remote backend's tags already face. Without it a driver
// could advertise a capability it does not implement, which is how the Type 4
// driver came to report CanLock while its MakeReadOnly was never written.
func TestPCSCTagsSatisfyCapabilityContract(t *testing.T) {
	for _, kind := range pcscTagKinds {
		tag := NewTagForType(kind, &stubTransport{}, "04112233445566")
		if tag == nil {
			t.Errorf("NewTagForType(%v) = nil", kind)
			continue
		}
		t.Run(tag.Type(), func(t *testing.T) {
			if err := AssertCapabilitiesConsistent(tag); err != nil {
				t.Errorf("%v: %v", kind, err)
			}
		})
	}
}

// TestPCSCTagsReportUnsupportedOperationsTypedly checks that an operation a tag
// does not support fails the way the rest of the system expects: a typed
// not-supported error. Reader.isPermanentWriteError, lockCard and the wire
// error mapping all branch on it, so an untyped error is reported to a client
// as UNKNOWN_ERROR and retried as if it might succeed.
func TestPCSCTagsReportUnsupportedOperationsTypedly(t *testing.T) {
	for _, kind := range pcscTagKinds {
		tag := NewTagForType(kind, &stubTransport{}, "04112233445566")
		caps := GetTagCapabilities(tag)

		t.Run(tag.Type(), func(t *testing.T) {
			if !caps.CanTransceive {
				if _, err := tag.Transceive([]byte{0x00}); !IsNotSupportedError(err) {
					t.Errorf("Transceive on a tag reporting CanTransceive=false: err = %v, want a not-supported error", err)
				}
			}
			if !caps.CanLock {
				if err := tag.MakeReadOnly(); !IsNotSupportedError(err) {
					t.Errorf("MakeReadOnly on a tag reporting CanLock=false: err = %v, want a not-supported error", err)
				}
			}
		})
	}
}

// TestPCSCTagsReportTechnology guards the technology every scan carries to
// clients. It used to be re-derived from the tag's display name, which had no
// case for NTAG, so every NTAG scan reported "Unknown".
func TestPCSCTagsReportTechnology(t *testing.T) {
	for _, kind := range pcscTagKinds {
		tag := NewTagForType(kind, &stubTransport{}, "04112233445566")

		t.Run(tag.Type(), func(t *testing.T) {
			if tech := GetTagCapabilities(tag).Technology; tech == "" {
				t.Error("Capabilities().Technology is empty")
			}
			card := NewCard(tag)
			if card.Technology == "" || card.Technology == "Unknown" {
				t.Errorf("Card.Technology = %q", card.Technology)
			}
			if want := GetTagCapabilities(tag).Technology; card.Technology != want {
				t.Errorf("Card.Technology = %q, want the tag's own %q", card.Technology, want)
			}
		})
	}
}
