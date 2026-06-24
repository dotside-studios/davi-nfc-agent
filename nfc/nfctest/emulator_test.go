package nfctest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// sampleNDEF is a minimal well-formed NDEF text record ("Hi", lang "en").
var sampleNDEF = []byte{0xD1, 0x01, 0x04, 0x54, 0x02, 0x65, 0x6E, 0x48, 0x69}

func textMessage(content string) *nfc.NDEFMessage {
	msg, err := (&nfc.NDEFMessageBuilder{
		Records: []nfc.NDEFRecordBuilder{&nfc.NDEFText{Content: content, Language: "en"}},
	}).Build()
	if err != nil {
		panic(err)
	}
	return msg
}

func memOf(c *EmulatedCard) *memEmulator { return c.transport.(*memEmulator) }

// --- Low-level emulator tests (real driver over emulated silicon) ----------

func TestKnownAnswer_PseudoAPDUFraming(t *testing.T) {
	if got, want := nfc.ReadBinaryAPDU(4, 4), []byte{0xFF, 0xB0, 0x00, 0x04, 0x04}; !bytes.Equal(got, want) {
		t.Errorf("READ APDU framing = % X", got)
	}
	writeCmd := nfc.UpdateBinaryAPDU(4, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	if want := []byte{0xFF, 0xD6, 0x00, 0x04, 0x04, 0xDE, 0xAD, 0xBE, 0xEF}; !bytes.Equal(writeCmd, want) {
		t.Errorf("WRITE APDU framing = % X", writeCmd)
	}

	e := newNTAGEmulator(nfc.DetectedNTAG215)
	if resp, _ := e.Transceive(writeCmd); !bytes.Equal(resp, []byte{0x90, 0x00}) {
		t.Errorf("write response = % X, want 90 00", resp)
	}
	resp, _ := e.Transceive(nfc.ReadBinaryAPDU(4, 4))
	if want := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x90, 0x00}; !bytes.Equal(resp, want) {
		t.Errorf("read response = % X", resp)
	}
}

func TestNTAGEmulator_WriteReadRoundTrip(t *testing.T) {
	for _, model := range []nfc.DetectedTagType{nfc.DetectedNTAG213, nfc.DetectedNTAG215, nfc.DetectedNTAG216} {
		e := newNTAGEmulator(model)
		tag := nfc.NewEmulatedTag(e, "04A1B2C3D4E5F6", model)
		if err := tag.WriteData(sampleNDEF); err != nil {
			t.Fatalf("model %d: WriteData: %v", model, err)
		}
		got, err := tag.ReadData()
		if err != nil {
			t.Fatalf("model %d: ReadData: %v", model, err)
		}
		if !bytes.Equal(got, sampleNDEF) {
			t.Errorf("model %d round-trip mismatch: % X", model, got)
		}
	}
}

func TestUltralightEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newUltralightEmulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralight)
	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch: % X", got)
	}
}

func TestUltralightEmulator_LockMakesUserPagesReadOnly(t *testing.T) {
	e := newUltralightEmulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralight)
	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}
	for _, page := range []int{4, 8, 15} {
		if e.tryWrite(page, []byte{1, 2, 3, 4}) {
			t.Errorf("page %d should be locked", page)
		}
	}
}

func TestNTAGEmulator_LockMakesAllUserPagesReadOnly(t *testing.T) {
	cases := []struct {
		model    nfc.DetectedTagType
		highPage int
	}{
		{nfc.DetectedNTAG213, 39},
		{nfc.DetectedNTAG215, 129},
		{nfc.DetectedNTAG216, 225},
	}
	for _, tc := range cases {
		e := newNTAGEmulator(tc.model)
		tag := nfc.NewEmulatedTag(e, "04A1B2C3D4E5F6", tc.model)
		if err := tag.MakeReadOnly(); err != nil {
			t.Fatalf("model %d: MakeReadOnly: %v", tc.model, err)
		}
		for _, page := range []int{4, 15, 16, tc.highPage} {
			if e.tryWrite(page, []byte{1, 2, 3, 4}) {
				t.Errorf("model %d: page %d writable after lock", tc.model, page)
			}
		}
	}
}

func TestClassicEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newClassicEmulator()
	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)
	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch: % X", got)
	}
}

func TestClassicEmulator_AuthRequiredForAccess(t *testing.T) {
	e := newClassicEmulator()
	tr := 1*4 + 3
	for i := 0; i < 6; i++ {
		e.blocks[tr][i] = 0x11
		e.blocks[tr][10+i] = 0x22
	}
	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)
	if _, err := tag.ReadData(); err == nil {
		t.Error("expected ReadData to fail with no default key for sector 1")
	}
}

func TestDESFireEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newDESFireEmulator()
	tag := nfc.NewEmulatedTag(e, "04DE5F1RE0", nfc.DetectedDESFire)
	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch: % X", got)
	}
}

func TestDESFireEmulator_ChainedReadWrite(t *testing.T) {
	e := newDESFireEmulator()
	tag := nfc.NewEmulatedTag(e, "04DE5F1RE0", nfc.DetectedDESFire)
	big := bytes.Repeat([]byte{0xAB}, 150) // spans ~3 frames
	if err := tag.WriteData(big); err != nil {
		t.Fatalf("chained WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("chained ReadData: %v", err)
	}
	if !bytes.Equal(got, big) {
		t.Errorf("chained round-trip mismatch: got %d bytes, want %d", len(got), len(big))
	}
}

func TestDESFire_WrappedStatusNotPlainISO(t *testing.T) {
	parsed, err := nfc.ParseAPDUResponse([]byte{0xDE, 0xAD, 0x91, 0x00})
	if err != nil {
		t.Fatalf("ParseAPDUResponse: %v", err)
	}
	if parsed.IsSuccess() {
		t.Error("wrapped DESFire OK (91 00) must not pass the generic ISO 90 00 check")
	}
}

// --- Façade: full reader pipeline over emulated silicon --------------------

func TestPipeline_NTAGWriteVerify(t *testing.T) {
	reader := NewEmulatedReader(t, NTAG215("04A1B2C3D4E5F6"))
	result, err := reader.WriteMessageWithResult(textMessage("emulated"), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the real driver")
	}
}

func TestPipeline_NTAGWriteThenLock(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6")
	reader := NewEmulatedReader(t, card)
	result, err := reader.WriteMessageWithResult(textMessage("lock me"), nfc.WriteOptions{Overwrite: true, Index: -1, Lock: true})
	if err != nil {
		t.Fatalf("write+lock: %v", err)
	}
	if !result.Verified || !result.Locked {
		t.Fatalf("expected verified+locked, got %+v", result)
	}
	for _, page := range []int{4, 16, 129} {
		if memOf(card).tryWrite(page, []byte{1, 2, 3, 4}) {
			t.Errorf("page %d writable after write+lock", page)
		}
	}
}

func TestPipeline_ClassicWriteVerify(t *testing.T) {
	reader := NewEmulatedReader(t, Classic1K("04112233"))
	result, err := reader.WriteMessageWithResult(textMessage("classic"), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the Classic driver")
	}
}

func TestPipeline_DESFireWriteVerify(t *testing.T) {
	reader := NewEmulatedReader(t, DESFire("04DE5F1RE0"))
	result, err := reader.WriteMessageWithResult(textMessage("desfire"), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the DESFire driver")
	}
}

func TestPipeline_NTAGLargePayloadMultiPage(t *testing.T) {
	reader := NewEmulatedReader(t, NTAG215("04A1B2C3D4E5F6"))
	result, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("a", 200)), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("multi-page write: %v", err)
	}
	if !result.Verified {
		t.Error("expected multi-page write to verify")
	}
}

func TestPipeline_ClassicMultiSector(t *testing.T) {
	reader := NewEmulatedReader(t, Classic1K("04112233"))
	result, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("y", 120)), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("multi-sector write: %v", err)
	}
	if !result.Verified {
		t.Error("expected multi-sector write to verify")
	}
}

func TestPipeline_OversizedWriteRejected(t *testing.T) {
	reader := NewEmulatedReader(t, NTAG213("04A1B2C3D4E5F6"))
	if _, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("z", 300)), nfc.WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("expected oversized write to be rejected")
	}
}

func TestPipeline_RetryRecoversFromTransientFailure(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6")
	memOf(card).failWrites = 1 // set before the reader (and its poll) exists
	reader := NewEmulatedReader(t, card)
	result, err := reader.WriteMessageWithResult(textMessage("retry me"), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("expected retry to recover: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified after retry")
	}
	if result.Attempts < 2 {
		t.Errorf("expected >=2 attempts, got %d", result.Attempts)
	}
}

func TestPipeline_VerificationCatchesBadWrite(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6")
	memOf(card).corrupt = true
	reader := NewEmulatedReader(t, card)
	if _, err := reader.WriteMessageWithResult(textMessage("oops"), nfc.WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("verification should have caught the corrupted write")
	}
}

func TestPipeline_WriteAfterLockFails(t *testing.T) {
	reader := NewEmulatedReader(t, NTAG215("04A1B2C3D4E5F6"))
	if _, err := reader.WriteMessageWithResult(textMessage("first"), nfc.WriteOptions{Overwrite: true, Index: -1, Lock: true}); err != nil {
		t.Fatalf("write+lock: %v", err)
	}
	if _, err := reader.WriteMessageWithResult(textMessage("second"), nfc.WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("expected a write to a locked tag to fail")
	}
}

func TestPipeline_EraseThroughReader(t *testing.T) {
	reader := NewEmulatedReader(t, NTAG215("04A1B2C3D4E5F6"))
	if _, err := reader.WriteMessageWithResult(textMessage("data"), nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	result, err := reader.EraseCard()
	if err != nil {
		t.Fatalf("EraseCard: %v", err)
	}
	if !result.Verified {
		t.Error("expected erase to be verified")
	}
}

// --- Façade DX showcase: a preloaded card consumed by the reader -----------

// TestFacade_PreloadThenAppendThroughReader shows the high-level flow: declare a
// card preloaded with content, present it to a reader, and have the reader read
// that content (resolveWriteMessage) to merge an appended record.
func TestFacade_PreloadThenAppendThroughReader(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("hello")
	reader := NewEmulatedReader(t, card)

	if _, err := reader.WriteMessageWithResult(textMessage("world"), nfc.WriteOptions{Overwrite: false, Index: -1}); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := card.Tag().ReadData()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	msg, err := nfc.DecodeNDEF(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := len(msg.Records()); got != 2 {
		t.Errorf("expected 2 records (preloaded + appended), got %d", got)
	}
}

// TestFacade_PresentAndRemove shows tap-on / tap-off semantics.
func TestFacade_PresentAndRemove(t *testing.T) {
	reader := NewEmulatedReader(t)

	reader.Present(NTAG215("04A1B2C3D4E5F6").WithText("hi"))
	if _, err := reader.WriteMessageWithResult(textMessage("x"), nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("write to presented card: %v", err)
	}

	reader.Remove("04A1B2C3D4E5F6")
	if _, err := reader.WriteMessageWithResult(textMessage("y"), nfc.WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("expected write to fail with no card present")
	}
}
