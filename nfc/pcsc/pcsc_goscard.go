//go:build !cgopcsc

// goscard backend: calls the platform's PC/SC library through purego, so no
// cgo and no C toolchain are involved.

package pcsc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ElMostafaIdrassi/goscard"
)

// Backend names the PC/SC implementation compiled in, for diagnostics.
const Backend = "goscard"

var initLib = sync.OnceValue(func() error {
	// goscard logs every call to stdout at info level unless told otherwise;
	// the agent does its own logging and errors come back as values anyway.
	if err := goscard.Initialize(goscard.NewDefaultLogger(goscard.LogLevelNone)); err != nil {
		return fmt.Errorf("PC/SC library unavailable: %w", err)
	}
	return nil
})

type goscardContext struct {
	ctx goscard.Context
}

type goscardCard struct {
	card goscard.Card
}

// EstablishContext establishes a PC/SC context, loading the platform's PC/SC
// library on first use.
func EstablishContext() (Context, error) {
	if err := initLib(); err != nil {
		return nil, err
	}

	ctx, ret, err := goscard.NewContext(goscard.SCardScopeSystem, nil, nil)
	if err != nil {
		return nil, statusError(uint32(ret), err)
	}
	return &goscardContext{ctx: ctx}, nil
}

func (c *goscardContext) ListReaders() ([]string, error) {
	readers, ret, err := c.ctx.ListReaders(nil)
	if err != nil {
		return nil, statusError(uint32(ret), err)
	}

	// goscard reports both "no readers attached" and "the daemon went away" by
	// status code alone, with a nil error. The first is not a failure; the
	// second is, and the manager relies on seeing it to notice that its context
	// is stale and establish a new one.
	switch code := uint32(ret); code {
	case 0, statusNoReadersAvailable:
		return readers, nil
	default:
		return nil, statusError(code, errors.New(goscard.PcscStringifyError(ret)))
	}
}

func (c *goscardContext) GetStatusChange(states []ReaderState, timeout time.Duration) error {
	sys := make([]goscard.SCardReaderState, len(states))
	for i, s := range states {
		if s.Reader == "" {
			return fmt.Errorf("reader name is empty")
		}
		sys[i] = goscard.SCardReaderState{
			// goscard hands the library a pointer to the raw bytes of this
			// string, and PC/SC reads it as a C string, so terminate it here.
			// The name it writes back is ignored below.
			Reader:       s.Reader + "\x00",
			CurrentState: goscard.SCardState(s.CurrentState),
			EventState:   goscard.SCardState(s.EventState),
			Atr:          hex.EncodeToString(s.Atr),
		}
	}

	ret, err := c.ctx.GetStatusChange(newTimeout(timeout), sys)
	if err != nil {
		return statusError(uint32(ret), err)
	}

	for i := range states {
		states[i].CurrentState = StateFlag(sys[i].CurrentState)
		states[i].EventState = StateFlag(sys[i].EventState)
		atr, err := hex.DecodeString(sys[i].Atr)
		if err != nil {
			return fmt.Errorf("reader %s: bad ATR %q: %w", states[i].Reader, sys[i].Atr, err)
		}
		states[i].Atr = atr
	}
	return nil
}

func newTimeout(d time.Duration) goscard.Timeout {
	// goscard turns a negative duration into a zero timeout; ours means block
	// forever, as it does everywhere else in PC/SC.
	if d < 0 {
		return goscard.NewInfiniteTimeout()
	}
	return goscard.NewTimeout(d)
}

func (c *goscardContext) Connect(reader string, mode ShareMode, proto Protocol) (Card, error) {
	card, ret, err := c.ctx.Connect(reader, goscard.SCardShareMode(mode), goscard.SCardProtocol(proto))
	if err != nil {
		return nil, statusError(uint32(ret), err)
	}
	return &goscardCard{card: card}, nil
}

func (c *goscardContext) Cancel() error {
	ret, err := c.ctx.Cancel()
	return statusError(uint32(ret), err)
}

func (c *goscardContext) Release() error {
	ret, err := c.ctx.Release()
	return statusError(uint32(ret), err)
}

func (c *goscardCard) ActiveProtocol() Protocol {
	return Protocol(c.card.ActiveProtocol())
}

func (c *goscardCard) Status() (*CardStatus, error) {
	status, ret, err := c.card.Status()
	if err != nil {
		return nil, statusError(uint32(ret), err)
	}

	atr, err := hex.DecodeString(status.Atr)
	if err != nil {
		return nil, fmt.Errorf("bad ATR %q: %w", status.Atr, err)
	}

	var reader string
	if len(status.ReaderNames) > 0 {
		reader = status.ReaderNames[0]
	}
	return &CardStatus{
		Reader:         reader,
		ActiveProtocol: Protocol(status.ActiveProtocol),
		Atr:            atr,
	}, nil
}

func (c *goscardCard) Transmit(cmd []byte) ([]byte, error) {
	// Unlike ebfe/scard, goscard wants the PCI structure for the active
	// protocol passed in explicitly.
	var pci *goscard.SCardIORequest
	switch proto := c.card.ActiveProtocol(); proto {
	case goscard.SCardProtocolT0:
		pci = &goscard.SCardIoRequestT0
	case goscard.SCardProtocolT1:
		pci = &goscard.SCardIoRequestT1
	default:
		return nil, fmt.Errorf("unsupported card protocol: %d", proto)
	}

	resp, ret, err := c.card.Transmit(pci, cmd, nil)
	if err != nil {
		return nil, statusError(uint32(ret), err)
	}
	return resp, nil
}

func (c *goscardCard) Disconnect(d Disposition) error {
	ret, err := c.card.Disconnect(goscard.SCardDisposition(d))
	return statusError(uint32(ret), err)
}
