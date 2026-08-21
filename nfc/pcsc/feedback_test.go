package pcsc

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// fakeCard is a scardCard that records what was sent on either channel and
// answers each with a configured result.
type fakeCard struct {
	controlResp []byte
	controlErr  error
	controls    [][]byte

	transmitResp []byte
	transmitErr  error
	transmits    [][]byte
}

func (c *fakeCard) ActiveProtocol() protocol { return protocolT1 }

func (c *fakeCard) Status() (*cardStatus, error) { return &cardStatus{}, nil }

func (c *fakeCard) Transmit(cmd []byte) ([]byte, error) {
	c.transmits = append(c.transmits, append([]byte(nil), cmd...))
	return c.transmitResp, c.transmitErr
}

func (c *fakeCard) Control(code uint32, in []byte) ([]byte, error) {
	if code != escapeControlCode {
		return nil, errors.New("unexpected control code")
	}
	c.controls = append(c.controls, append([]byte(nil), in...))
	return c.controlResp, c.controlErr
}

func (c *fakeCard) Disconnect(disposition) error { return nil }

// acr122Device builds a device for an ACR122U holding the given card, without
// the PC/SC context a real one is opened against.
func acr122Device(card scardCard) *device {
	return &device{card: card, readerName: "ACS ACR122U PICC Interface 00 00"}
}

func TestSignalPrefersControl(t *testing.T) {
	card := &fakeCard{controlResp: []byte{0x90, 0x00}}
	dev := acr122Device(card)

	if err := dev.Signal(nfc.SignalSuccess); err != nil {
		t.Fatalf("Signal() = %v, want nil", err)
	}

	if len(card.controls) != 1 {
		t.Fatalf("sent %d escape commands, want 1", len(card.controls))
	}
	if want := acr122Success.command(); !bytes.Equal(card.controls[0], want) {
		t.Errorf("escape command = % X, want % X", card.controls[0], want)
	}
	if len(card.transmits) != 0 {
		t.Errorf("sent %d pseudo-APDUs, want none once the escape command worked", len(card.transmits))
	}
	if dev.feedback != feedbackControl {
		t.Errorf("transport = %v, want feedbackControl", dev.feedback)
	}

	// The settled transport is the only one tried from here on.
	if err := dev.Signal(nfc.SignalFailure); err != nil {
		t.Fatalf("second Signal() = %v, want nil", err)
	}
	if len(card.controls) != 2 || len(card.transmits) != 0 {
		t.Errorf("second signal sent %d escape commands and %d pseudo-APDUs, want 2 and 0",
			len(card.controls), len(card.transmits))
	}
	if want := acr122Failure.command(); !bytes.Equal(card.controls[1], want) {
		t.Errorf("escape command = % X, want % X", card.controls[1], want)
	}
}

func TestSignalFallsBackToTransmit(t *testing.T) {
	tests := []struct {
		name       string
		controlErr error
	}{
		// What pcsc-lite reports when escape commands are not authorised, and
		// what the Windows CCID class driver reports without
		// EscapeCommandEnable set.
		{"escape not authorised", errNotTransacted},
		{"escape not supported", errNotSupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := &fakeCard{
				controlErr:   tt.controlErr,
				transmitResp: []byte{0x90, 0x00},
			}
			dev := acr122Device(card)

			if err := dev.Signal(nfc.SignalSuccess); err != nil {
				t.Fatalf("Signal() = %v, want nil", err)
			}
			if len(card.transmits) != 1 {
				t.Fatalf("sent %d pseudo-APDUs, want 1", len(card.transmits))
			}
			if want := acr122Success.command(); !bytes.Equal(card.transmits[0], want) {
				t.Errorf("pseudo-APDU = % X, want % X", card.transmits[0], want)
			}
			if dev.feedback != feedbackTransmit {
				t.Errorf("transport = %v, want feedbackTransmit", dev.feedback)
			}

			if err := dev.Signal(nfc.SignalSuccess); err != nil {
				t.Fatalf("second Signal() = %v, want nil", err)
			}
			if len(card.controls) != 1 {
				t.Errorf("tried the escape command %d times, want 1", len(card.controls))
			}
		})
	}
}

func TestSignalUnsupportedWhenNeitherChannelCarries(t *testing.T) {
	card := &fakeCard{
		controlErr:   errNotTransacted,
		transmitResp: []byte{0x63, 0x00},
	}
	dev := acr122Device(card)

	err := dev.Signal(nfc.SignalSuccess)
	if !nfc.IsNotSupportedError(err) {
		t.Fatalf("Signal() = %v, want a not-supported error", err)
	}
	if dev.feedback != feedbackUnavailable {
		t.Errorf("transport = %v, want feedbackUnavailable", dev.feedback)
	}

	// Nothing is sent once the reader is known not to answer.
	if err := dev.Signal(nfc.SignalSuccess); !nfc.IsNotSupportedError(err) {
		t.Fatalf("second Signal() = %v, want a not-supported error", err)
	}
	if len(card.controls) != 1 || len(card.transmits) != 1 {
		t.Errorf("sent %d escape commands and %d pseudo-APDUs, want 1 each",
			len(card.controls), len(card.transmits))
	}
}

func TestSignalCardRemovedLeavesTransportUnsettled(t *testing.T) {
	card := &fakeCard{controlErr: errNotTransacted, transmitErr: errRemovedCard}
	dev := acr122Device(card)

	err := dev.Signal(nfc.SignalSuccess)
	if !nfc.IsCardRemovedError(err) {
		t.Fatalf("Signal() = %v, want a card-removed error", err)
	}
	if dev.feedback != feedbackUntried {
		t.Errorf("transport = %v, want it left untried", dev.feedback)
	}
}

func TestSignalUnsupportedReader(t *testing.T) {
	card := &fakeCard{controlResp: []byte{0x90, 0x00}}
	dev := &device{card: card, readerName: "Identiv uTrust 3700 F CL Reader"}

	if err := dev.Signal(nfc.SignalSuccess); !nfc.IsNotSupportedError(err) {
		t.Fatalf("Signal() = %v, want a not-supported error", err)
	}
	if len(card.controls) != 0 || len(card.transmits) != 0 {
		t.Error("sent a command to a reader that has no LED or buzzer")
	}
}

func TestSignalWithoutCardConnection(t *testing.T) {
	dev := acr122Device(nil)

	if err := dev.Signal(nfc.SignalSuccess); !nfc.IsCardRemovedError(err) {
		t.Fatalf("Signal() = %v, want a card-removed error", err)
	}
}
