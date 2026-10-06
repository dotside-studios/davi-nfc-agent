package pcsc

import "sync/atomic"

// Observer is told about a reader's traffic, so a hardware test can record
// exactly what the reader saw. It carries plain types only, which lets a
// recorder in another package satisfy it without importing this one.
//
// Nothing in the agent sets an Observer. A hook that is not set costs one
// atomic load per opened reader.
type Observer struct {
	// Connected is called once a reader has been opened for a card, after the
	// probe for framing-level exchange has settled. method names how the reader
	// carries one: "acr122" (Direct Transmit carrying PN532 InCommunicateThru),
	// "part3" (a PC/SC Part 3 transparent session) or "none".
	Connected func(reader string, atr []byte, method string, canTransceiveRaw bool)

	// Transmit is called for every command sent to a connected card, with the
	// bytes exactly as the reader's driver saw them and what it answered. The
	// slices are copies.
	Transmit func(reader string, command, response []byte, err error)
}

var observer atomic.Pointer[Observer]

// SetObserver installs o for every reader opened from now on, and removes the
// observer when o is the zero value. A device already open keeps the observer
// it was opened under.
func SetObserver(o Observer) {
	if o.Connected == nil && o.Transmit == nil {
		observer.Store(nil)
		return
	}
	observer.Store(&o)
}

// observedCard forwards to a scardCard and reports each Transmit to an
// Observer.
type observedCard struct {
	scardCard
	reader string
	obs    *Observer
}

// observe wraps card for the observer installed now, or returns it unchanged
// with a nil observer.
func observe(card scardCard, reader string) (scardCard, *Observer) {
	obs := observer.Load()
	if obs == nil {
		return card, nil
	}
	return &observedCard{scardCard: card, reader: reader, obs: obs}, obs
}

func (c *observedCard) Transmit(cmd []byte) ([]byte, error) {
	resp, err := c.scardCard.Transmit(cmd)
	if c.obs.Transmit != nil {
		c.obs.Transmit(c.reader, append([]byte(nil), cmd...), append([]byte(nil), resp...), err)
	}
	return resp, err
}

// rawMethod names how a device carries a framing-level exchange, for an
// Observer.
func (d *device) rawMethod() string {
	switch d.rawMode() {
	case rawACR122:
		return "acr122"
	case rawTransparent:
		return "part3"
	default:
		return "none"
	}
}

// announce tells obs a reader has been opened.
func (d *device) announce(obs *Observer) {
	if obs == nil || obs.Connected == nil {
		return
	}
	obs.Connected(d.readerName, append([]byte(nil), d.atr...), d.rawMethod(), d.SupportsTransceiveRaw())
}
