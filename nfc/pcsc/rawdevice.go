package pcsc

import (
	"errors"
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rawMode is how a reader carries a framing-level exchange.
type rawMode int32

const (
	// rawUnknown means the reader has not been asked.
	rawUnknown rawMode = iota

	// rawUnsupported means it was asked and offers neither method.
	rawUnsupported

	// rawACR122 means Direct Transmit carrying PN532 InCommunicateThru.
	rawACR122

	// rawTransparent means a PC/SC Part 3 transparent exchange session.
	rawTransparent
)

// rawProbes remembers the outcome of probing each reader by name. A manager
// opens a new device for every card, and a reader's answer does not change with
// the card, so it is asked once.
type rawProbes struct {
	modes sync.Map // reader name -> rawMode
}

func (p *rawProbes) load(reader string) (rawMode, bool) {
	if p == nil {
		return rawUnknown, false
	}
	v, ok := p.modes.Load(reader)
	if !ok {
		return rawUnknown, false
	}
	return v.(rawMode), true
}

func (p *rawProbes) store(reader string, mode rawMode) {
	if p != nil {
		p.modes.Store(reader, mode)
	}
}

// probeRawMode settles how this reader carries a framing-level exchange. An
// ACR122 class reader is known by name. Any other is asked once, with the
// command that starts a transparent session, and only a reader that accepts it
// is taken to offer the exchange: a reader that refuses, or answers in a way
// that is not a Part 3 answer, is not guessed at. The session is ended again at
// once.
//
// The caller must be the only user of the device, as newDevice is.
func (d *device) probeRawMode(probes *rawProbes) {
	if isACR122(d.readerName) {
		d.raw.Store(int32(rawACR122))
		return
	}
	if mode, ok := probes.load(d.readerName); ok {
		d.raw.Store(int32(mode))
		return
	}

	resp, err := d.card.Transmit(transparentStartSession())
	if err != nil {
		// A card that left mid-probe says nothing about the reader, so leave
		// it unasked for the next card.
		if !isCardRemovedPCSCError(err) {
			d.settleRaw(probes, rawUnsupported)
		}
		return
	}
	if _, err := parseTransparent(resp); err != nil {
		pcscLog.Printf("Reader %s does not take a transparent session (%v), so raw frames are not supported on it", d.readerName, err)
		d.settleRaw(probes, rawUnsupported)
		return
	}
	if _, err := d.card.Transmit(transparentEndSession()); err != nil {
		pcscWarn.Printf("Reader %s: ending the probe transparent session: %v", d.readerName, err)
	}
	d.settleRaw(probes, rawTransparent)
}

func (d *device) settleRaw(probes *rawProbes, mode rawMode) {
	d.raw.Store(int32(mode))
	probes.store(d.readerName, mode)
}

func (d *device) rawMode() rawMode {
	return rawMode(d.raw.Load())
}

// SupportsTransceiveRaw reports whether this reader carries a framing-level
// exchange (implements nfc.DeviceRawTransceiver). It answers from what the
// probe found and sends nothing.
func (d *device) SupportsTransceiveRaw() bool {
	mode := d.rawMode()
	return mode == rawACR122 || mode == rawTransparent
}

// TransceiveRaw sends frame to the tag as it is and returns the tag's reply
// (implements nfc.RawCardTransport). The reader adds the CRC and checks the
// tag's.
func (d *device) TransceiveRaw(frame []byte) ([]byte, error) {
	mode := d.rawMode()
	if mode != rawACR122 && mode != rawTransparent {
		return nil, nfc.NewNotSupportedError("TransceiveRaw")
	}

	if d.cardRemoved != nil {
		select {
		case <-d.cardRemoved:
			d.mu.Lock()
			d.tag = nil
			d.mu.Unlock()
			return nil, nfc.NewCardRemovedError(fmt.Errorf("card removed (detected by monitor)"))
		default:
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card == nil {
		d.tag = nil
		return nil, nfc.NewCardRemovedError(fmt.Errorf("device not connected"))
	}

	var (
		reply []byte
		err   error
	)
	if mode == rawACR122 {
		reply, err = d.exchangeThru(frame)
	} else {
		reply, err = d.exchangeTransparent(frame)
	}
	if err != nil {
		if nfc.IsCardRemovedError(err) {
			d.tag = nil
		}
		return nil, err
	}
	return reply, nil
}

// transmitLocked sends a command to the card, reporting a card that left as
// such. The caller holds the device lock.
func (d *device) transmitLocked(cmd []byte) ([]byte, error) {
	resp, err := d.card.Transmit(cmd)
	if err != nil {
		if isCardRemovedPCSCError(err) {
			return nil, nfc.NewCardRemovedError(err)
		}
		return nil, fmt.Errorf("pcsc device transceive: %w", err)
	}
	return resp, nil
}

// exchangeThru carries a frame through an ACR122's Direct Transmit.
func (d *device) exchangeThru(frame []byte) ([]byte, error) {
	cmd, err := acr122Thru(frame)
	if err != nil {
		return nil, err
	}
	resp, err := d.transmitLocked(cmd)
	if err != nil {
		return nil, err
	}
	reply, err := acr122Unthru(resp)
	if err != nil {
		return nil, d.rawFailure(err)
	}
	return reply, nil
}

// exchangeTransparent carries a frame through a transparent session of its own:
// started, used once and ended, so a failed exchange never leaves the reader
// out of its normal mode.
func (d *device) exchangeTransparent(frame []byte) ([]byte, error) {
	exchange, err := transparentTransceive(frame)
	if err != nil {
		return nil, err
	}

	resp, err := d.transmitLocked(transparentStartSession())
	if err != nil {
		return nil, err
	}
	if _, err := parseTransparent(resp); err != nil {
		return nil, d.rawFailure(fmt.Errorf("starting a transparent session: %w", err))
	}
	defer func() {
		if _, endErr := d.card.Transmit(transparentEndSession()); endErr != nil && !isCardRemovedPCSCError(endErr) {
			pcscWarn.Printf("Reader %s: ending the transparent session: %v", d.readerName, endErr)
		}
	}()

	resp, err = d.transmitLocked(exchange)
	if err != nil {
		return nil, err
	}
	reply, err := transparentReply(resp)
	if err != nil {
		return nil, d.rawFailure(err)
	}
	return reply, nil
}

// rawFailure shapes a reader's refusal of a framing-level exchange. A PN532
// status for a card that disappeared is a removal, which the reader must learn
// of; any other is a failed exchange.
func (d *device) rawFailure(err error) error {
	var status pn532Status
	if errors.As(err, &status) && status.code == 0x2B {
		return nfc.NewCardRemovedError(err)
	}
	return nfc.NewTransceiveError("TransceiveRaw", err)
}
