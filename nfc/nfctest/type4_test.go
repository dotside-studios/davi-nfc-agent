package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// A message written through the Type 4 driver reads back intact. Both halves go
// through the driver's SELECT of the NDEF application (by name) and its CC and
// NDEF files (by identifier); the emulator enforces the Type 4 selection rules,
// so this round trip fails if the driver builds either SELECT wrongly — which is
// exactly the bug fixed alongside this emulator (SelectFileAPDU using the by-name
// P1 for a file identifier).
func TestType4WriteReadRoundTrip(t *testing.T) {
	tag := Type4("04A1B2C3D4E5F6").Tag()

	built, err := (&nfc.NDEFMessageBuilder{
		Records: []nfc.NDEFRecordBuilder{&nfc.NDEFText{Content: "hello type 4", Language: "en"}},
	}).Build()
	if err != nil {
		t.Fatalf("build NDEF: %v", err)
	}
	data, err := built.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}

	if err := tag.WriteData(data); err != nil {
		t.Fatalf("WriteData through the Type 4 driver failed (the SELECT it builds is likely wrong): %v", err)
	}

	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData through the Type 4 driver failed: %v", err)
	}

	back, err := nfc.DecodeNDEF(got)
	if err != nil {
		t.Fatalf("decode NDEF read back: %v", err)
	}
	text, err := back.GetText()
	if err != nil {
		t.Fatalf("GetText: %v", err)
	}
	if text != "hello type 4" {
		t.Errorf("round-trip text = %q, want %q", text, "hello type 4")
	}
}

// The emulator selects an elementary file only by identifier (P1=0x00, P2=0x0C),
// as a compliant Type 4 tag does, and refuses the by-name form (P1=0x04) that
// belongs to an application select. Without this the round trip above could pass
// against a lenient emulator even with a wrong driver SELECT, so this pins the
// emulator's own strictness.
func TestType4EmulatorSelectionRules(t *testing.T) {
	e := newType4Emulator()

	if sw := swOf(e.Transceive(nfc.SelectFileByAIDAPDU(type4NDEFAppAID))); sw != 0x9000 {
		t.Fatalf("select NDEF application: SW = %04X, want 9000", sw)
	}

	// The correct EF select — what the fixed SelectFileAPDU builds.
	if sw := swOf(e.Transceive(nfc.SelectFileAPDU([]byte{0xE1, 0x03}))); sw != 0x9000 {
		t.Errorf("select CC by identifier: SW = %04X, want 9000", sw)
	}

	// The old by-name form for a file identifier must be refused.
	byName := []byte{0x00, 0xA4, 0x04, 0x00, 0x02, 0xE1, 0x03, 0x00}
	if sw := swOf(e.Transceive(byName)); sw == 0x9000 {
		t.Errorf("emulator accepted a by-name select of file E103 (SW 9000); a Type 4 tag refuses it")
	}

	// A file identifier selected with the wrong P2 is refused too.
	wrongP2 := []byte{0x00, 0xA4, 0x00, 0x00, 0x02, 0xE1, 0x03}
	if sw := swOf(e.Transceive(wrongP2)); sw == 0x9000 {
		t.Errorf("emulator accepted an EF select with P2=00 (SW 9000); Type 4 requires P2=0C")
	}
}

// swOf reads the status word off an emulator response.
func swOf(resp []byte, err error) uint16 {
	if err != nil || len(resp) < 2 {
		return 0
	}
	return uint16(resp[len(resp)-2])<<8 | uint16(resp[len(resp)-1])
}
