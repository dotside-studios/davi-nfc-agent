package nfctest

import (
	"bytes"
	"context"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rawExchange sends a framing-level frame through the reader, as a transceive
// with raw set does.
func rawExchange(r *EmulatedReader, uid string, frame ...byte) ([]byte, error) {
	return r.TransceiveTag(context.Background(), "", uid, frame, true)
}

func TestRawFrames_NTAGAcceptsFramingLevelExchange(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	r := NewEmulatedReader(t, NTAG215(uid))

	sig, err := rawExchange(r, uid, 0x3C, 0x00)
	if err != nil {
		t.Fatalf("READ_SIG: %v", err)
	}
	if !bytes.Equal(sig, emuSignature()) {
		t.Errorf("READ_SIG = % X, want the emulator's signature", sig)
	}

	if ack, err := rawExchange(r, uid, 0xA2, 0x10, 1, 2, 3, 4); err != nil || !bytes.Equal(ack, []byte{frameACK}) {
		t.Fatalf("WRITE = % X, %v, want the 4-bit ACK", ack, err)
	}
	page, err := rawExchange(r, uid, 0x30, 0x10)
	if err != nil {
		t.Fatalf("READ: %v", err)
	}
	if !bytes.Equal(page[:4], []byte{1, 2, 3, 4}) || len(page) != 16 {
		t.Errorf("READ page 16 = % X, want 16 bytes opening 01 02 03 04", page)
	}

	fast, err := rawExchange(r, uid, 0x3A, 0x10, 0x11)
	if err != nil {
		t.Fatalf("FAST_READ: %v", err)
	}
	if !bytes.Equal(fast[:4], []byte{1, 2, 3, 4}) || len(fast) != 8 {
		t.Errorf("FAST_READ pages 16-17 = % X, want 8 bytes opening 01 02 03 04", fast)
	}

	if cnt, err := rawExchange(r, uid, 0x39, 0x02); err != nil || len(cnt) != 3 {
		t.Errorf("READ_CNT = % X, %v, want 3 bytes", cnt, err)
	}
}

func TestRawFrames_UltralightAcceptsFramingLevelExchange(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	r := NewEmulatedReader(t, Ultralight(uid))

	if page, err := rawExchange(r, uid, 0x30, 0x04); err != nil || len(page) != 16 {
		t.Fatalf("READ = % X, %v, want 16 bytes", page, err)
	}
	if ack, err := rawExchange(r, uid, 0xA2, 0x05, 9, 8, 7, 6); err != nil || !bytes.Equal(ack, []byte{frameACK}) {
		t.Fatalf("WRITE = % X, %v", ack, err)
	}
	// An original Ultralight has no signature to read.
	if _, err := rawExchange(r, uid, 0x3C, 0x00); err == nil {
		t.Error("an Ultralight answered READ_SIG")
	}
}

func TestRawFrames_TagNAKIsAnErrorNotAReply(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	r := NewEmulatedReader(t, NTAG215(uid))

	// Page 0 is the UID, which no frame may write.
	if reply, err := rawExchange(r, uid, 0xA2, 0x00, 1, 2, 3, 4); err == nil {
		t.Errorf("a write to page 0 answered % X", reply)
	}
}

func TestRawFrames_LockedPageRefusesAWrite(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	r := NewEmulatedReader(t, NTAG215(uid))

	// The static lock bytes are OR-only, so setting bit 4 locks page 4.
	if _, err := rawExchange(r, uid, 0xA2, 0x02, 0x00, 0x00, 0x10, 0x00); err != nil {
		t.Fatalf("locking page 4: %v", err)
	}
	if _, err := rawExchange(r, uid, 0xA2, 0x04, 1, 2, 3, 4); err == nil {
		t.Error("a locked page took a write")
	}
}

func TestRawFrames_ReaderOfAnotherFamilyRefuses(t *testing.T) {
	const uid = "04A1B2C3"
	r := NewEmulatedReader(t, Classic1K(uid))

	_, err := rawExchange(r, uid, 0x30, 0x00)
	if !nfc.IsNotSupportedError(err) {
		t.Errorf("err = %v, want a not-supported error, not an APDU", err)
	}
}

func TestRawFrames_APDUExchangeStillRefusedOnType2(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	r := NewEmulatedReader(t, NTAG215(uid))

	_, err := r.TransceiveTag(context.Background(), "", uid, []byte{0x30, 0x00}, false)
	if !nfc.IsNotSupportedError(err) {
		t.Errorf("err = %v, want a not-supported error", err)
	}
}

func TestRawFrames_DriverTagsAcceptFrames(t *testing.T) {
	for name, card := range map[string]*EmulatedCard{
		"NTAG213":     NTAG213("04A1B2C3D4E5F6"),
		"NTAG216":     NTAG216("04A1B2C3D4E5F6"),
		"Ultralight":  Ultralight("04A1B2C3D4E5F6"),
		"UltralightC": UltralightC("04A1B2C3D4E5F6"),
	} {
		t.Run(name, func(t *testing.T) {
			raw, ok := card.Tag().(nfc.TagRawTransceiver)
			if !ok {
				t.Fatalf("%T does not offer a framing-level exchange", card.Tag())
			}
			if reply, err := raw.TransceiveRaw([]byte{0x30, 0x04}); err != nil || len(reply) != 16 {
				t.Errorf("READ = % X, %v, want 16 bytes", reply, err)
			}
		})
	}
}

// The Direct Transmit answer is the PN532 framing an ACR122 gives, so the wrap
// and unwrap in the PC/SC driver can be run against it; see nfc/pcsc.
func TestRawFrames_DirectTransmitAnswersInPN532Framing(t *testing.T) {
	e := newNTAGEmulator(nfc.DetectedNTAG215)

	resp, err := e.Transceive([]byte{0xFF, 0x00, 0x00, 0x00, 0x04, 0xD4, 0x42, 0x3C, 0x00})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	want := append([]byte{0xD5, 0x43, 0x00}, emuSignature()...)
	want = append(want, 0x90, 0x00)
	if !bytes.Equal(resp, want) {
		t.Errorf("answer = % X, want % X", resp, want)
	}

	resp, err = e.Transceive([]byte{0xFF, 0x00, 0x00, 0x00, 0x04, 0xD4, 0x42, 0x3D, 0x00})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if want := []byte{0xD5, 0x43, 0x01, 0x90, 0x00}; !bytes.Equal(resp, want) {
		t.Errorf("answer to an unknown frame = % X, want % X", resp, want)
	}

	resp, err = e.Transceive([]byte{0xFF, 0x00, 0x00, 0x00, 0x03, 0xD4, 0x4A, 0x01})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if !bytes.Equal(resp, emuFail()) {
		t.Errorf("a PN532 command other than InCommunicateThru answered % X, want the failure status", resp)
	}
}
