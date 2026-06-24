package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func classicEmu(c *EmulatedCard) *classicEmulator { return c.transport.(*classicEmulator) }

// classicKeySetter is the subset of the Classic driver used to configure extra
// authentication keys (SetCandidateKeys is exported on the unexported tag type).
type classicKeySetter interface {
	SetCandidateKeys(keys [][]byte)
}

// TestClassicEmulator_CustomKeyAuthentication verifies the Classic driver can
// read and write a card provisioned with non-default keys once those keys are
// configured — and that it correctly fails when they are not.
func TestClassicEmulator_CustomKeyAuthentication(t *testing.T) {
	e := newClassicEmulator()
	custom := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	e.rekeyAll(custom)

	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)

	// With only the built-in default keys, authentication must fail.
	if err := tag.WriteData(sampleNDEF); err == nil {
		t.Fatal("expected auth failure before configuring the custom key")
	}

	kc, ok := tag.(classicKeySetter)
	if !ok {
		t.Fatal("Classic tag should accept candidate keys")
	}
	kc.SetCandidateKeys([][]byte{custom})

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData with custom key: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData with custom key: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch: % X", got)
	}
}

// TestPipeline_ClassicCustomKeysThroughReader verifies NFCReader.SetClassicKeys
// is injected into the tag so a non-default-keyed card can be written and
// verified through the full reader pipeline.
func TestPipeline_ClassicCustomKeysThroughReader(t *testing.T) {
	card := Classic1K("04112233")
	custom := []byte{0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6}
	classicEmu(card).rekeyAll(custom)

	reader := NewEmulatedReader(t, card)
	reader.SetClassicKeys([][]byte{custom})

	result, err := reader.WriteMessageWithResult(textMessage("custom keyed"),
		nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("write with custom keys: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the reader with custom keys")
	}
}

// TestPipeline_ClassicForceInitializeFormatsBlankCard verifies that a blank
// Classic card written with ForceInitialize is formatted for NDEF: the MAD is
// written in sector 0, sector trailers are switched to the NFC Forum / MAD keys,
// and the NDEF message round-trips.
func TestPipeline_ClassicForceInitializeFormatsBlankCard(t *testing.T) {
	card := Classic1K("04112233")
	reader := NewEmulatedReader(t, card)

	result, err := reader.WriteMessageWithResult(textMessage("formatted"),
		nfc.WriteOptions{Overwrite: true, Index: -1, ForceInitialize: true})
	if err != nil {
		t.Fatalf("force-initialize write: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write after formatting")
	}

	e := classicEmu(card)

	// Sector 0 trailer Key A switched to the MAD key.
	sector0Trailer := e.block(3)
	if !bytes.Equal(sector0Trailer[0:6], nfc.KeyMAD) {
		t.Errorf("sector 0 trailer Key A = % X, want MAD key % X", sector0Trailer[0:6], nfc.KeyMAD)
	}
	// MAD GPB byte marks a v1 multi-application card.
	if sector0Trailer[9] != 0xC1 {
		t.Errorf("MAD GPB = 0x%02X, want 0xC1", sector0Trailer[9])
	}

	// Data sector 1 trailer Key A switched to the NFC Forum public key.
	sector1Trailer := e.block(7)
	if !bytes.Equal(sector1Trailer[0:6], nfc.KeyNFCForum) {
		t.Errorf("sector 1 trailer Key A = % X, want NFC Forum key % X", sector1Trailer[0:6], nfc.KeyNFCForum)
	}
	if sector1Trailer[9] != 0x40 {
		t.Errorf("NDEF data sector GPB = 0x%02X, want 0x40", sector1Trailer[9])
	}

	// The MAD marks data sector 1 as NFC Forum NDEF (AID 0x03E1).
	madBlock1 := e.block(1)
	if madBlock1[2] != 0x03 || madBlock1[3] != 0xE1 {
		t.Errorf("MAD AID for sector 1 = % X, want 03 E1", madBlock1[2:4])
	}
}

// TestClassicEmulator_ForceInitializeReadsBackThroughDefaultKeys verifies that
// after formatting, the card is readable using only the built-in default keys
// (the NFC Forum key is among them), i.e. a freshly formatted card behaves like
// any other NDEF Classic card.
func TestClassicEmulator_ForceInitializeReadsBackThroughDefaultKeys(t *testing.T) {
	e := newClassicEmulator()
	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)

	aw, ok := tag.(nfc.AdvancedWriter)
	if !ok {
		t.Fatal("Classic tag should implement AdvancedWriter")
	}
	if err := aw.WriteDataWithOptions(sampleNDEF, nfc.TagWriteOptions{ForceInitialize: true}); err != nil {
		t.Fatalf("format write: %v", err)
	}

	// A fresh tag with only default keys (no custom keys) must read it back.
	fresh := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)
	got, err := fresh.ReadData()
	if err != nil {
		t.Fatalf("ReadData after format: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("post-format round-trip mismatch: % X", got)
	}
}
