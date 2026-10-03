package pcsc

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
)

// The frames and answers below are constructed from the PN532 User Manual
// (InCommunicateThru, section 7.3.9, and the status byte of section 7.1), the
// ACS ACR122U API (Direct Transmit) and the PC/SC Part 3 supplement as the
// author recalls them. None was captured from a reader.

func TestACR122Thru_WrapsAsDirectTransmit(t *testing.T) {
	got, err := acr122Thru([]byte{0x3C, 0x00})
	if err != nil {
		t.Fatalf("acr122Thru: %v", err)
	}
	want := []byte{0xFF, 0x00, 0x00, 0x00, 0x04, 0xD4, 0x42, 0x3C, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("wrapped = % X, want % X", got, want)
	}
}

func TestACR122Thru_FrameLengthLimits(t *testing.T) {
	if _, err := acr122Thru(nil); err == nil {
		t.Error("an empty frame was wrapped")
	}

	longest := bytes.Repeat([]byte{0xA2}, maxThruFrame)
	got, err := acr122Thru(longest)
	if err != nil {
		t.Fatalf("the longest frame was refused: %v", err)
	}
	if got[4] != 0xFF {
		t.Errorf("Lc = %02X, want FF", got[4])
	}
	if _, err := acr122Thru(append(longest, 0x00)); err == nil {
		t.Error("a frame too long for Lc was wrapped")
	}
}

func TestACR122Unthru(t *testing.T) {
	tests := []struct {
		name    string
		resp    []byte
		want    []byte
		wantErr string
	}{
		{"reply", []byte{0xD5, 0x43, 0x00, 0x01, 0x02, 0x90, 0x00}, []byte{0x01, 0x02}, ""},
		{"empty reply", []byte{0xD5, 0x43, 0x00, 0x90, 0x00}, []byte{}, ""},
		{"more information bit is not an error", []byte{0xD5, 0x43, 0x40, 0x07, 0x90, 0x00}, []byte{0x07}, ""},
		{"timeout", []byte{0xD5, 0x43, 0x01, 0x90, 0x00}, nil, "timeout"},
		{"crc error", []byte{0xD5, 0x43, 0x02, 0x90, 0x00}, nil, "CRC"},
		{"card disappeared", []byte{0xD5, 0x43, 0x2B, 0x90, 0x00}, nil, "disappeared"},
		{"unnamed code", []byte{0xD5, 0x43, 0x3E, 0x90, 0x00}, nil, "PN532 status 3E"},
		{"reader reports the chip failed", []byte{0x63, 0x00}, nil, "6300"},
		{"not an InCommunicateThru answer", []byte{0xD5, 0x41, 0x00, 0x90, 0x00}, nil, "not an InCommunicateThru"},
		{"syntax error answer", []byte{0xD5, 0x7F, 0x01, 0x90, 0x00}, nil, "not an InCommunicateThru"},
		{"no status byte", []byte{0xD5, 0x43, 0x90, 0x00}, nil, "not an InCommunicateThru"},
		{"too short", []byte{0x90}, nil, "status word"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := acr122Unthru(tt.resp)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("acr122Unthru: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("reply = % X, want % X", got, tt.want)
			}
		})
	}
}

func TestACR122Unthru_StatusErrorIsTyped(t *testing.T) {
	_, err := acr122Unthru([]byte{0xD5, 0x43, 0x2B, 0x90, 0x00})
	var status pn532Status
	if !errors.As(err, &status) || status.code != 0x2B {
		t.Errorf("err = %v, want a pn532Status 2B", err)
	}
}

func TestTransparentCommands(t *testing.T) {
	if got, want := transparentStartSession(), []byte{0xFF, 0xC2, 0x00, 0x00, 0x02, 0x81, 0x00}; !bytes.Equal(got, want) {
		t.Errorf("start session = % X, want % X", got, want)
	}
	if got, want := transparentEndSession(), []byte{0xFF, 0xC2, 0x00, 0x00, 0x02, 0x82, 0x00}; !bytes.Equal(got, want) {
		t.Errorf("end session = % X, want % X", got, want)
	}

	got, err := transparentTransceive([]byte{0x3C, 0x00})
	if err != nil {
		t.Fatalf("transparentTransceive: %v", err)
	}
	// Timer object 5F46 (250000 us = 0003D090), then transceive object 95.
	want := []byte{0xFF, 0xC2, 0x00, 0x01, 0x0B, 0x5F, 0x46, 0x04, 0x00, 0x03, 0xD0, 0x90, 0x95, 0x02, 0x3C, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("transceive = % X, want % X", got, want)
	}

	long, err := transparentTransceive(bytes.Repeat([]byte{0x01}, 200))
	if err != nil {
		t.Fatalf("a 200 byte frame: %v", err)
	}
	if !bytes.Equal(long[12:15], []byte{0x95, 0x81, 200}) {
		t.Errorf("long form length = % X, want 95 81 C8", long[12:15])
	}
	if _, err := transparentTransceive(bytes.Repeat([]byte{0x01}, 250)); err == nil {
		t.Error("data objects past Lc were built")
	}
	if _, err := transparentTransceive(nil); err == nil {
		t.Error("an empty frame was built")
	}
}

func TestParseTransparent(t *testing.T) {
	ok := []byte{0xC0, 0x03, 0x00, 0x90, 0x00, 0x90, 0x00}
	if _, err := parseTransparent(ok); err != nil {
		t.Errorf("a successful start answer: %v", err)
	}

	for name, resp := range map[string][]byte{
		"status word":       {0x6D, 0x00},
		"error object":      {0xC0, 0x03, 0x01, 0x63, 0x00, 0x90, 0x00},
		"object past end":   {0x97, 0x05, 0x01, 0x90, 0x00},
		"cut-short tag":     {0x5F, 0x90, 0x00},
		"no length":         {0x97, 0x90, 0x00},
		"too short":         {0x90},
		"cut-short length":  {0x97, 0x81, 0x90, 0x00},
		"two-byte tag ends": {0x5F, 0x46, 0x90, 0x00},
	} {
		if _, err := parseTransparent(resp); err == nil {
			t.Errorf("%s: an answer % X was accepted", name, resp)
		}
	}
}

func TestTransparentReply(t *testing.T) {
	got, err := transparentReply([]byte{0xC0, 0x03, 0x00, 0x90, 0x00, 0x96, 0x02, 0x00, 0x00, 0x97, 0x03, 0xAA, 0xBB, 0xCC, 0x90, 0x00})
	if err != nil || !bytes.Equal(got, []byte{0xAA, 0xBB, 0xCC}) {
		t.Errorf("reply = % X, %v, want AA BB CC", got, err)
	}

	got, err = transparentReply([]byte{0xC0, 0x03, 0x00, 0x90, 0x00, 0x90, 0x00})
	if err != nil || len(got) != 0 {
		t.Errorf("reply with no data object = % X, %v, want empty", got, err)
	}

	long := bytes.Repeat([]byte{0x11}, 150)
	resp := append([]byte{0x97, 0x81, 150}, long...)
	resp = append(resp, 0x90, 0x00)
	if got, err := transparentReply(resp); err != nil || !bytes.Equal(got, long) {
		t.Errorf("long form reply = % X, %v", got, err)
	}
}

// scriptCard is a scardCard whose Transmit is a function, and which records
// every command it was sent.
type scriptCard struct {
	fn    func(cmd []byte) ([]byte, error)
	sent  [][]byte
	proto protocol
}

func (c *scriptCard) ActiveProtocol() protocol {
	if c.proto == 0 {
		return protocolT1
	}
	return c.proto
}
func (c *scriptCard) Status() (*cardStatus, error) { return &cardStatus{}, nil }
func (c *scriptCard) Transmit(cmd []byte) ([]byte, error) {
	c.sent = append(c.sent, append([]byte(nil), cmd...))
	return c.fn(cmd)
}
func (c *scriptCard) Control(uint32, []byte) ([]byte, error) { return nil, errNotSupported }
func (c *scriptCard) Disconnect(disposition) error           { return nil }

func (c *scriptCard) sentWith(prefix ...byte) int {
	n := 0
	for _, cmd := range c.sent {
		if bytes.HasPrefix(cmd, prefix) {
			n++
		}
	}
	return n
}

// acr122Card is an ACR122 holding an emulated NTAG: it forwards the pseudo-APDUs
// it is sent to the emulator, which answers Direct Transmit as the PN532 does.
func acr122Card(emu nfc.CardTransport) *scriptCard {
	return &scriptCard{fn: func(cmd []byte) ([]byte, error) { return emu.Transceive(cmd) }}
}

func TestDeviceTransceiveRaw_ACR122AgainstEmulatedNTAG(t *testing.T) {
	card := nfctest.NTAG215("04A1B2C3D4E5F6")
	dev := &device{card: acr122Card(card.Transport()), readerName: "ACS ACR122U PICC Interface 00 00"}
	dev.probeRawMode(nil)

	if !dev.SupportsTransceiveRaw() {
		t.Fatal("an ACR122 does not report raw support")
	}

	sig, err := dev.TransceiveRaw([]byte{0x3C, 0x00})
	if err != nil {
		t.Fatalf("READ_SIG: %v", err)
	}
	if len(sig) != 32 {
		t.Errorf("READ_SIG returned %d bytes, want 32", len(sig))
	}

	if _, err := dev.TransceiveRaw([]byte{0xA2, 0x20, 0xCA, 0xFE, 0xBA, 0xBE}); err != nil {
		t.Fatalf("WRITE: %v", err)
	}
	page, err := dev.TransceiveRaw([]byte{0x30, 0x20})
	if err != nil || !bytes.HasPrefix(page, []byte{0xCA, 0xFE, 0xBA, 0xBE}) {
		t.Errorf("READ page 32 = % X, %v", page, err)
	}

	// An unknown frame is the PN532's timeout status, which is an error.
	_, err = dev.TransceiveRaw([]byte{0x3D, 0x00})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("unknown frame: err = %v, want a timeout status", err)
	}
	if got := nfc.GetErrorCode(err); got != nfc.ErrCodeTransceiveFailed {
		t.Errorf("error code = %v, want a transceive failure", got)
	}
}

func TestDeviceTransceiveRaw_ACR122CardDisappearedIsRemoval(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) {
		return []byte{0xD5, 0x43, 0x2B, 0x90, 0x00}, nil
	}}
	dev := &device{card: card, readerName: "ACS ACR122U PICC Interface 00 00"}
	dev.probeRawMode(nil)

	if _, err := dev.TransceiveRaw([]byte{0x30, 0x00}); !nfc.IsCardRemovedError(err) {
		t.Errorf("err = %v, want a card-removed error", err)
	}
}

func TestDeviceTransceiveRaw_TransmitRemovalIsReported(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return nil, errRemovedCard }}
	dev := &device{card: card, readerName: "ACS ACR122U PICC Interface 00 00"}
	dev.probeRawMode(nil)

	if _, err := dev.TransceiveRaw([]byte{0x30, 0x00}); !nfc.IsCardRemovedError(err) {
		t.Errorf("err = %v, want a card-removed error", err)
	}
}

func TestACR122Probe_SendsNothing(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return nil, errors.New("unexpected") }}
	dev := &device{card: card, readerName: "ACS ACR122U PICC Interface 00 00"}

	dev.probeRawMode(nil)

	if len(card.sent) != 0 {
		t.Errorf("probing an ACR122 sent %d commands, want none", len(card.sent))
	}
}

// transparentCard answers the Part 3 commands as the author understands the
// supplement, and forwards the transceive object to an emulated tag.
func transparentCard(emu nfc.RawCardTransport) *scriptCard {
	return &scriptCard{fn: func(cmd []byte) ([]byte, error) {
		if len(cmd) < 6 || cmd[0] != 0xFF || cmd[1] != 0xC2 {
			return []byte{0x6D, 0x00}, nil
		}
		objects, err := transparentObjects(cmd[5:])
		if err != nil {
			return []byte{0x67, 0x00}, nil
		}
		switch cmd[3] {
		case 0x00:
			return []byte{0xC0, 0x03, 0x00, 0x90, 0x00, 0x90, 0x00}, nil
		case 0x01:
			reply, err := emu.TransceiveRaw(objects[uint16(objTransceive)])
			if err != nil {
				return []byte{0xC0, 0x03, 0x01, 0x63, 0x00, 0x90, 0x00}, nil
			}
			out := []byte{0xC0, 0x03, 0x00, 0x90, 0x00, 0x97, byte(len(reply))}
			out = append(out, reply...)
			return append(out, 0x90, 0x00), nil
		}
		return []byte{0x6A, 0x86}, nil
	}}
}

func TestDeviceTransceiveRaw_TransparentSessionAgainstEmulatedNTAG(t *testing.T) {
	emu := nfctest.NTAG215("04A1B2C3D4E5F6").Transport().(nfc.RawCardTransport)
	card := transparentCard(emu)
	probes := &rawProbes{}
	dev := &device{card: card, readerName: "ACS ACR1252 PICC Reader"}
	dev.probeRawMode(probes)

	if !dev.SupportsTransceiveRaw() {
		t.Fatal("a reader that accepted a transparent session does not report raw support")
	}
	if mode := dev.rawMode(); mode != rawTransparent {
		t.Errorf("mode = %v, want rawTransparent", mode)
	}
	probeEnds := card.sentWith(0xFF, 0xC2, 0x00, 0x00, 0x02, 0x82)
	if probeEnds != 1 {
		t.Errorf("the probe left %d session end commands, want 1", probeEnds)
	}

	sig, err := dev.TransceiveRaw([]byte{0x3C, 0x00})
	if err != nil {
		t.Fatalf("READ_SIG: %v", err)
	}
	if len(sig) != 32 {
		t.Errorf("READ_SIG returned %d bytes, want 32", len(sig))
	}
	if starts, ends := card.sentWith(0xFF, 0xC2, 0x00, 0x00, 0x02, 0x81), card.sentWith(0xFF, 0xC2, 0x00, 0x00, 0x02, 0x82); starts != ends {
		t.Errorf("%d sessions started, %d ended", starts, ends)
	}

	// A frame the tag refuses fails, and the session is still ended.
	if _, err := dev.TransceiveRaw([]byte{0x3D, 0x00}); err == nil {
		t.Error("a refused frame was reported as a reply")
	}
	if starts, ends := card.sentWith(0xFF, 0xC2, 0x00, 0x00, 0x02, 0x81), card.sentWith(0xFF, 0xC2, 0x00, 0x00, 0x02, 0x82); starts != ends {
		t.Errorf("after a failed exchange: %d sessions started, %d ended", starts, ends)
	}

	// The probe is remembered for the next card on this reader.
	next := &scriptCard{fn: func([]byte) ([]byte, error) { return nil, errors.New("unexpected") }}
	again := &device{card: next, readerName: "ACS ACR1252 PICC Reader"}
	again.probeRawMode(probes)
	if len(next.sent) != 0 {
		t.Errorf("a reader already probed was probed again with %d commands", len(next.sent))
	}
	if !again.SupportsTransceiveRaw() {
		t.Error("the remembered probe was lost")
	}
}

func TestProbeRawMode_ReaderWithoutTransparentSessionIsNotClaimed(t *testing.T) {
	for name, answer := range map[string][]byte{
		"instruction not supported": {0x6D, 0x00},
		"wrong class":               {0x6E, 0x00},
		"error object":              {0xC0, 0x03, 0x01, 0x63, 0x00, 0x90, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			card := &scriptCard{fn: func([]byte) ([]byte, error) { return answer, nil }}
			probes := &rawProbes{}
			dev := &device{card: card, readerName: "Some Other Reader"}
			dev.probeRawMode(probes)

			if dev.SupportsTransceiveRaw() {
				t.Error("support was claimed for a reader that refused the session")
			}
			if mode, ok := probes.load("Some Other Reader"); !ok || mode != rawUnsupported {
				t.Errorf("remembered = %v, %v, want rawUnsupported", mode, ok)
			}
			if _, err := dev.TransceiveRaw([]byte{0x3C, 0x00}); !nfc.IsNotSupportedError(err) {
				t.Errorf("TransceiveRaw err = %v, want a not-supported error", err)
			}
			if got := card.sentWith(0xFF, 0xC2); got != 1 {
				t.Errorf("sent %d Part 3 commands, want only the start", got)
			}
		})
	}
}

// A bare success to the start command is a Part 3 answer with no error object,
// which is what a reader that has nothing to add would send.
func TestProbeRawMode_BareSuccessIsAccepted(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return []byte{0x90, 0x00}, nil }}
	dev := &device{card: card, readerName: "Some Other Reader"}

	dev.probeRawMode(&rawProbes{})

	if !dev.SupportsTransceiveRaw() {
		t.Error("a bare success to start session was refused")
	}
}

func TestProbeRawMode_CardRemovedLeavesReaderUnasked(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return nil, errRemovedCard }}
	probes := &rawProbes{}
	dev := &device{card: card, readerName: "Some Other Reader"}

	dev.probeRawMode(probes)

	if dev.rawMode() != rawUnknown {
		t.Errorf("mode = %v, want rawUnknown", dev.rawMode())
	}
	if _, ok := probes.load("Some Other Reader"); ok {
		t.Error("a probe that never reached the reader was remembered")
	}
	if dev.SupportsTransceiveRaw() {
		t.Error("support was claimed without an answer")
	}
}

// The tag drivers reach the reader through nfc.RawCardTransport, so what the
// PC/SC device offers is what NTAG and Ultralight drivers get.
func TestDriversRunThroughThePCSCDevice(t *testing.T) {
	dev := &device{card: acr122Card(nfctest.NTAG215("04A1B2C3D4E5F6").Transport()), readerName: "ACS ACR122U PICC Interface 00 00"}
	dev.probeRawMode(nil)

	for name, kind := range map[string]nfc.DetectedTagType{
		"NTAG":       nfc.DetectedNTAG215,
		"Ultralight": nfc.DetectedUltralight,
		"Classic":    nfc.DetectedClassic1K,
	} {
		tag := nfc.NewEmulatedTag(dev, "04A1B2C3D4E5F6", kind)
		raw, ok := tag.(nfc.TagRawTransceiver)
		if !ok {
			t.Fatalf("%s: %T does not offer a framing-level exchange", name, tag)
		}
		if reply, err := raw.TransceiveRaw([]byte{0x30, 0x04}); err != nil || len(reply) != 16 {
			t.Errorf("%s: READ = % X, %v, want 16 bytes", name, reply, err)
		}
		if _, err := tag.Transceive([]byte{0x30, 0x04}); name != "Classic" && !nfc.IsNotSupportedError(err) {
			t.Errorf("%s: an APDU exchange err = %v, want it still refused", name, err)
		}
	}
}

func TestDriverRefusesRawOnAReaderWithoutIt(t *testing.T) {
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return []byte{0x6D, 0x00}, nil }}
	dev := &device{card: card, readerName: "Some Other Reader"}
	dev.probeRawMode(&rawProbes{})
	card.sent = nil

	tag := nfc.NewEmulatedTag(dev, "04A1B2C3D4E5F6", nfc.DetectedNTAG215)
	_, err := tag.(nfc.TagRawTransceiver).TransceiveRaw([]byte{0x3C, 0x00})
	if !nfc.IsNotSupportedError(err) {
		t.Errorf("err = %v, want a not-supported error", err)
	}
	if len(card.sent) != 0 {
		t.Errorf("a refused raw exchange sent %d commands to the reader", len(card.sent))
	}
}

func TestDeviceCapabilitiesReportRawSupport(t *testing.T) {
	acr := &device{readerName: "ACS ACR122U PICC Interface 00 00"}
	acr.raw.Store(int32(rawACR122))
	if caps := nfc.BuildDeviceCapabilities(acr); !caps.CanTransceiveRaw || !caps.CanTransceive {
		t.Errorf("ACR122 capabilities = %+v, want CanTransceiveRaw and CanTransceive", caps)
	}

	plain := &device{readerName: "Some Other Reader"}
	plain.raw.Store(int32(rawUnsupported))
	if caps := nfc.BuildDeviceCapabilities(plain); caps.CanTransceiveRaw {
		t.Errorf("a reader with no raw method reports CanTransceiveRaw: %+v", caps)
	}
}

func TestDevicesListingReportsRawForACR122ByName(t *testing.T) {
	m := &Manager{ctx: &fakeContext{readers: []string{"ACS ACR122U PICC Interface 00 00", "Generic Reader"}}}

	listings, err := m.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(listings) != 2 {
		t.Fatalf("got %d listings, want 2", len(listings))
	}
	if !listings[0].Capabilities.CanTransceiveRaw {
		t.Error("the ACR122 listing does not report raw support")
	}
	if listings[1].Capabilities.CanTransceiveRaw {
		t.Error("a reader not yet probed reports raw support")
	}
}

// fakeContext is a scardContext that lists readers and does nothing else.
type fakeContext struct {
	readers []string
}

func (c *fakeContext) ListReaders() ([]string, error) { return c.readers, nil }
func (c *fakeContext) GetStatusChange([]readerState, time.Duration) error {
	return errTimeout
}
func (c *fakeContext) Connect(string, shareMode, protocol) (scardCard, error) {
	return nil, errNoSmartcard
}
func (c *fakeContext) Cancel() error  { return nil }
func (c *fakeContext) Release() error { return nil }
