package nfc_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
)

// stubTransport is a card that answers nothing. The contract checks below never
// exchange data with it: they ask what a tag claims and then call only the
// operations it says it does not support.
type stubTransport struct{}

func (*stubTransport) Transceive([]byte) ([]byte, error) { return nil, nil }
func (*stubTransport) IsCardPresent() bool               { return true }

// drivenKinds is every tag kind nfc.NewTagForType builds a driver for. The
// emulator-backed suite in nfctest covers the families that can be driven
// through real I/O; this one covers all of them, including the kinds no
// emulator stands in for yet.
var drivenKinds = []nfc.DetectedTagType{
	nfc.DetectedClassic1K,
	nfc.DetectedClassic4K,
	nfc.DetectedUltralight,
	nfc.DetectedUltralightC,
	nfc.DetectedUltralightEV1,
	nfc.DetectedUltralightEV1_128,
	nfc.DetectedNTAG213,
	nfc.DetectedNTAG215,
	nfc.DetectedNTAG216,
	nfc.DetectedDESFire,
	nfc.DetectedISO14443_4,
}

// TestEveryDriverKeepsTheContract runs the drivers through the contract both
// backends share, so a driver cannot advertise a capability it does not back.
func TestEveryDriverKeepsTheContract(t *testing.T) {
	for _, kind := range drivenKinds {
		tag := nfc.NewTagForType(kind, &stubTransport{}, "04112233445566")
		if tag == nil {
			t.Errorf("NewTagForType(%v) = nil", kind)
			continue
		}
		t.Run(tag.Type(), func(t *testing.T) {
			nfctest.AssertTagContract(t, tag)
		})
	}
}
