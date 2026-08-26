package pcsc

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// feedbackTransport is the channel a reader's LED and buzzer commands travel
// on. Which one works depends on the machine's PC/SC stack rather than on the
// reader, so it is settled on the first signal and remembered.
type feedbackTransport int

const (
	// feedbackUntried means no signal has been sent on this connection yet.
	feedbackUntried feedbackTransport = iota

	// feedbackControl means SCardControl carries the commands.
	feedbackControl

	// feedbackTransmit means they travel as pseudo-APDUs over the card
	// connection instead.
	feedbackTransmit

	// feedbackUnavailable means neither channel carried them.
	feedbackUnavailable
)

// Signal flashes the reader's LED and sounds its buzzer, implementing
// nfc.FeedbackDevice.
//
// It blocks for as long as the reader takes to play the sequence, around
// 200 ms for a success and 400 ms for a failure, because the reader answers
// only once it has finished. Callers in a hurry should signal in the
// background.
//
// A reader with no LED or buzzer, and one whose commands neither channel
// carries, both report a not-supported error.
func (d *device) Signal(s nfc.Signal) error {
	if !hasACR122LEDBuzzer(d.readerName) {
		return nfc.NewNotSupportedError("Signal")
	}

	var blink acr122Blink
	switch s {
	case nfc.SignalSuccess:
		blink = acr122Success
	case nfc.SignalFailure:
		blink = acr122Failure
	default:
		return fmt.Errorf("pcsc device signal: unknown signal %q", s)
	}
	cmd := blink.command()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card == nil {
		return nfc.NewCardRemovedError(fmt.Errorf("device not connected"))
	}

	switch d.feedback {
	case feedbackControl:
		return d.signalOverControl(cmd)
	case feedbackTransmit:
		return d.signalOverTransmit(cmd)
	case feedbackUnavailable:
		return nfc.NewNotSupportedError("Signal")
	}

	// First signal on this connection. SCardControl is the channel meant for
	// reader commands and leaves the card session alone, so it is tried first;
	// a stack that will not carry escape commands still leaves the pseudo-APDU,
	// which an ACR122 answers while a card is connected. Whichever works is
	// remembered, so later signals cost one call.
	controlErr := d.signalOverControl(cmd)
	if controlErr == nil {
		d.feedback = feedbackControl
		return nil
	}

	transmitErr := d.signalOverTransmit(cmd)
	if transmitErr == nil {
		d.feedback = feedbackTransmit
		pcscLog.Printf("Reader %s: sending LED and buzzer commands over the card connection, because this machine's PC/SC stack would not carry them as escape commands (%v)", d.readerName, controlErr)
		return nil
	}

	if nfc.IsCardRemovedError(transmitErr) {
		// The card left mid-signal, which says nothing about the reader, so
		// leave the transport unsettled for the next signal to decide.
		return nfc.NewCardRemovedError(transmitErr)
	}

	d.feedback = feedbackUnavailable
	return &nfc.NFCError{
		Code:    nfc.ErrCodeNotSupported,
		Op:      "Signal",
		Message: fmt.Sprintf("reader %s took neither an escape command (%v) nor a pseudo-APDU", d.readerName, controlErr),
		Cause:   transmitErr,
	}
}

// signalOverControl sends a reader command over SCardControl. Caller holds the
// device lock.
func (d *device) signalOverControl(cmd []byte) error {
	resp, err := d.card.Control(escapeControlCode, cmd)
	if err != nil {
		return fmt.Errorf("escape command: %w", err)
	}
	return checkACR122Response(resp)
}

// signalOverTransmit sends a reader command as a pseudo-APDU over the card
// connection. Caller holds the device lock.
//
// A card that leaves mid-signal is reported as removed here rather than by the
// caller, so that a reader refusing the command is not mistaken for one.
func (d *device) signalOverTransmit(cmd []byte) error {
	resp, err := d.card.Transmit(cmd)
	if err != nil {
		if isCardRemovedPCSCError(err) {
			return nfc.NewCardRemovedError(err)
		}
		return fmt.Errorf("pseudo-APDU: %w", err)
	}
	return checkACR122Response(resp)
}
