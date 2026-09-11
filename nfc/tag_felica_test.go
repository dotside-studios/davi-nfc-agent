package nfc

import (
	"testing"
)

// A FeliCa carries its identity and says plainly that it has nothing else to
// give, which is what makes it an identity-only scan rather than a failure.
func TestFeliCaTagIsIdentityOnly(t *testing.T) {
	tag := NewTagForType(DetectedFeliCa, &stubCardTransport{}, "0123456789ABCDEF")
	if tag == nil {
		t.Fatal("NewTagForType(DetectedFeliCa) = nil")
	}

	if got := tag.UID(); got != "0123456789ABCDEF" {
		t.Errorf("UID() = %q, want the IDm the reader reported", got)
	}
	if got := tag.Type(); got != CardTypeFeliCa {
		t.Errorf("Type() = %q, want %q", got, CardTypeFeliCa)
	}

	caps := GetTagCapabilities(tag)
	if caps.Technology != "ISO18092" {
		t.Errorf("Technology = %q, want ISO18092", caps.Technology)
	}
	if caps.SupportsNDEF || caps.CanWrite || caps.CanLock || caps.CanTransceive {
		t.Errorf("claims something it cannot do: %+v", caps)
	}

	if _, err := tag.ReadData(); !IsNoPayloadError(err) {
		t.Errorf("ReadData() = %v, want a no-payload error", err)
	}

	// The pairing the identity-only scan depends on: no NDEF, and a read that
	// says so rather than returning nothing.
	if err := AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}

// A FeliCa decoded from the wire, with only its type name to go on, is
// described the same way as one on a reader.
func TestInferredFeliCaCapabilities(t *testing.T) {
	caps := InferTagCapabilities(CardTypeFeliCa)

	if caps.Technology != "ISO18092" || caps.TagFamily != "FeliCa" {
		t.Errorf("Technology = %q, TagFamily = %q, want ISO18092 and FeliCa", caps.Technology, caps.TagFamily)
	}
	if caps.SupportsNDEF || caps.CanWrite || caps.CanTransceive || caps.CanLock {
		t.Errorf("claims something the driver cannot do: %+v", caps)
	}
}

// stubCardTransport answers nothing. The FeliCa driver sends no command, so it
// is never consulted; it exists to satisfy the factory's signature.
type stubCardTransport struct{}

func (*stubCardTransport) Transceive([]byte) ([]byte, error) { return nil, nil }
func (*stubCardTransport) IsCardPresent() bool               { return true }
