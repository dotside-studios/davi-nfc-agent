//go:build cgopcsc

// ebfe/scard backend: the historical cgo binding, kept as a fallback for the
// goscard backend. Selected with -tags cgopcsc, which needs libpcsclite-dev on
// Linux and the PCSC framework on macOS. See docs/pure-go-pcsc.md.

package pcsc

import (
	"errors"
	"time"

	"github.com/ebfe/scard"
)

// Backend names the PC/SC implementation compiled in, for diagnostics.
const Backend = "ebfe/scard"

type ebfeContext struct {
	ctx *scard.Context
}

type ebfeCard struct {
	card *scard.Card
}

// EstablishContext establishes a PC/SC context.
func EstablishContext() (Context, error) {
	ctx, err := scard.EstablishContext()
	if err != nil {
		return nil, convertError(err)
	}
	return &ebfeContext{ctx: ctx}, nil
}

func (c *ebfeContext) ListReaders() ([]string, error) {
	readers, err := c.ctx.ListReaders()
	if err != nil {
		// ebfe/scard reports a reader-less machine as an error; the adapter
		// reports it as an empty list, the way the goscard backend does.
		if errors.Is(err, scard.Error(statusNoReadersAvailable)) {
			return nil, nil
		}
		return nil, convertError(err)
	}
	return readers, nil
}

func (c *ebfeContext) GetStatusChange(states []ReaderState, timeout time.Duration) error {
	sys := make([]scard.ReaderState, len(states))
	for i, s := range states {
		sys[i] = scard.ReaderState{
			Reader:       s.Reader,
			CurrentState: scard.StateFlag(s.CurrentState),
			EventState:   scard.StateFlag(s.EventState),
			Atr:          s.Atr,
		}
	}

	if err := c.ctx.GetStatusChange(sys, timeout); err != nil {
		return convertError(err)
	}

	for i := range states {
		states[i].CurrentState = StateFlag(sys[i].CurrentState)
		states[i].EventState = StateFlag(sys[i].EventState)
		states[i].Atr = sys[i].Atr
	}
	return nil
}

func (c *ebfeContext) Connect(reader string, mode ShareMode, proto Protocol) (Card, error) {
	card, err := c.ctx.Connect(reader, scard.ShareMode(mode), scard.Protocol(proto))
	if err != nil {
		return nil, convertError(err)
	}
	return &ebfeCard{card: card}, nil
}

func (c *ebfeContext) Cancel() error {
	return convertError(c.ctx.Cancel())
}

func (c *ebfeContext) Release() error {
	return convertError(c.ctx.Release())
}

func (c *ebfeCard) ActiveProtocol() Protocol {
	return Protocol(c.card.ActiveProtocol())
}

func (c *ebfeCard) Status() (*CardStatus, error) {
	status, err := c.card.Status()
	if err != nil {
		return nil, convertError(err)
	}
	return &CardStatus{
		Reader:         status.Reader,
		ActiveProtocol: Protocol(status.ActiveProtocol),
		Atr:            status.Atr,
	}, nil
}

func (c *ebfeCard) Transmit(cmd []byte) ([]byte, error) {
	resp, err := c.card.Transmit(cmd)
	if err != nil {
		return nil, convertError(err)
	}
	return resp, nil
}

func (c *ebfeCard) Disconnect(d Disposition) error {
	return convertError(c.card.Disconnect(scard.Disposition(d)))
}

// convertError turns a scard.Error into our Error so that the sentinels in
// pcsc.go match. Anything else is passed through.
func convertError(err error) error {
	if err == nil {
		return nil
	}
	var scardErr scard.Error
	if errors.As(err, &scardErr) {
		return statusError(uint32(scardErr), err)
	}
	return err
}
