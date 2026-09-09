package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

const ntag424UID = "04A1B2C3D4E5F6"

// An NTAG 424 DNA reads and writes NDEF through the Type 4 driver, unchanged.
// The card adds no NDEF behaviour of its own, so the point of the round trip is
// that naming the card did not cost the driver it rides on.
func TestNTAG424WriteReadRoundTrip(t *testing.T) {
	tag := NTAG424(ntag424UID).Tag()

	built, err := (&nfc.NDEFMessageBuilder{
		Records: []nfc.NDEFRecordBuilder{&nfc.NDEFURI{Content: "https://davi.social/t/01"}},
	}).Build()
	if err != nil {
		t.Fatalf("build NDEF: %v", err)
	}
	data, err := built.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}

	if err := tag.WriteData(data); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	back, err := nfc.DecodeNDEF(got)
	if err != nil {
		t.Fatalf("decode NDEF read back: %v", err)
	}
	uri, err := back.GetURI()
	if err != nil {
		t.Fatalf("GetURI: %v", err)
	}
	if uri != "https://davi.social/t/01" {
		t.Errorf("round-trip URI = %q, want %q", uri, "https://davi.social/t/01")
	}
}

// The card names itself, and reports the memory and NDEF ceiling of an NTAG 424
// DNA rather than the unknowns a generic Type 4 tag reports. The capacity is the
// part that matters beyond cosmetics: it is what lets an oversized write be
// refused before it reaches the card.
func TestNTAG424ReportsItsOwnIdentity(t *testing.T) {
	tag := NTAG424(ntag424UID).Tag()

	if got := tag.Type(); got != nfc.CardTypeNtag424 {
		t.Errorf("Type() = %q, want %q", got, nfc.CardTypeNtag424)
	}

	caps := nfc.GetTagCapabilities(tag)
	if caps.MemorySize != 416 {
		t.Errorf("MemorySize = %d, want 416", caps.MemorySize)
	}
	if caps.MaxNDEFSize != 254 {
		t.Errorf("MaxNDEFSize = %d, want 254", caps.MaxNDEFSize)
	}
	if !caps.CanWrite || !caps.CanTransceive {
		t.Errorf("CanWrite = %v, CanTransceive = %v, want both true", caps.CanWrite, caps.CanTransceive)
	}
	if caps.CanLock {
		t.Error("CanLock is true, but locking an NTAG 424 is not implemented")
	}
	if !caps.SupportsAuthentication {
		t.Error("SupportsAuthentication is false, but the card carries AES keys")
	}

	// A generic Type 4 tag stays generic: naming one card must not rename the
	// rest.
	if got := Type4(ntag424UID).Tag().Type(); got != nfc.CardTypeType4 {
		t.Errorf("Type4 tag Type() = %q, want %q", got, nfc.CardTypeType4)
	}
}

// Capabilities and behaviour agree: what the card says it can do is what its
// methods actually do.
func TestNTAG424CapabilitiesAreHonest(t *testing.T) {
	if err := nfc.AssertCapabilitiesConsistent(NTAG424(ntag424UID).Tag()); err != nil {
		t.Fatal(err)
	}
}

// The emulator answers the wrapped GET_VERSION as the card does, and a plain
// Type 4 tag refuses it. Detection reads exactly this difference, so pinning it
// keeps the emulator honest about what it is standing in for.
func TestNTAG424EmulatorAnswersWrappedGetVersion(t *testing.T) {
	e := newNTAG424Emulator(ntag424UID)

	resp, err := e.Transceive(nfc.NTAG424GetVersionAPDU())
	if err != nil {
		t.Fatalf("GET_VERSION: %v", err)
	}
	kind, ok := nfc.ParseWrappedGetVersionResponse(resp)
	if !ok || kind != nfc.DetectedNTAG424 {
		t.Fatalf("version response identified kind %v (ok=%v), want DetectedNTAG424", kind, ok)
	}
	if sw := swOf(resp, nil); sw != 0x91AF {
		t.Errorf("first frame SW = %04X, want 91AF (more frames follow)", sw)
	}

	// The remaining frames follow, the last one ending the chain.
	if sw := swOf(e.Transceive(nfc.DESFireWrapAPDU(nfc.DFCmdAdditionalFrame, nil))); sw != 0x91AF {
		t.Errorf("second frame SW = %04X, want 91AF", sw)
	}
	if sw := swOf(e.Transceive(nfc.DESFireWrapAPDU(nfc.DFCmdAdditionalFrame, nil))); sw != 0x9100 {
		t.Errorf("third frame SW = %04X, want 9100 (chain complete)", sw)
	}

	// A plain Type 4 tag does not implement the command at all.
	if _, ok := nfc.ParseWrappedGetVersionResponse(mustTransceive(t, newType4Emulator())); ok {
		t.Error("a plain Type 4 emulator was identified from GET_VERSION; it does not implement the command")
	}
}

func mustTransceive(t *testing.T, e *type4Emulator) []byte {
	t.Helper()
	resp, err := e.Transceive(nfc.NTAG424GetVersionAPDU())
	if err != nil {
		t.Fatalf("GET_VERSION against the Type 4 emulator: %v", err)
	}
	return resp
}
