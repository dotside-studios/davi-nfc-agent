// Package pcsc is the agent's PC/SC adapter.
//
// It exposes the small slice of the PC/SC API the reader path actually uses —
// eight calls and a handful of status codes — so that the library behind it can
// be swapped without touching the rest of nfc/.
//
// The default backend is goscard, which resolves the platform's PC/SC library
// at runtime (winscard.dll, libpcsclite.so.1, PCSC.framework) and needs no cgo,
// so the agent builds with CGO_ENABLED=0 and cross-compiles without a C
// toolchain. Building with -tags cgopcsc selects the previous ebfe/scard
// binding instead.
//
// The constants below carry the same numeric values as the PC/SC SCARD_*
// definitions, so both backends map onto them by conversion rather than by
// lookup table.
package pcsc

import (
	"fmt"
	"time"
)

// StateFlag is a reader state bitmask (SCARD_STATE_*). Its upper 16 bits carry
// the reader's event counter, which pcsc-lite increments on every card
// insertion or removal.
type StateFlag uint32

const (
	StateUnaware     StateFlag = 0x0000
	StateIgnore      StateFlag = 0x0001
	StateChanged     StateFlag = 0x0002
	StateUnknown     StateFlag = 0x0004
	StateUnavailable StateFlag = 0x0008
	StateEmpty       StateFlag = 0x0010
	StatePresent     StateFlag = 0x0020
	StateAtrmatch    StateFlag = 0x0040
	StateExclusive   StateFlag = 0x0080
	StateInuse       StateFlag = 0x0100
	StateMute        StateFlag = 0x0200
	StateUnpowered   StateFlag = 0x0400
)

// Protocol is a card transmission protocol (SCARD_PROTOCOL_*).
type Protocol uint32

const (
	ProtocolUndefined Protocol = 0x0000
	ProtocolT0        Protocol = 0x0001
	ProtocolT1        Protocol = 0x0002
	ProtocolAny       Protocol = ProtocolT0 | ProtocolT1
)

// ShareMode says how the reader is shared with other applications
// (SCARD_SHARE_*).
type ShareMode uint32

const (
	ShareExclusive ShareMode = 0x0001
	ShareShared    ShareMode = 0x0002
	ShareDirect    ShareMode = 0x0003
)

// Disposition says what to do with the card on disconnect (SCARD_*_CARD).
type Disposition uint32

const (
	LeaveCard   Disposition = 0x0000
	ResetCard   Disposition = 0x0001
	UnpowerCard Disposition = 0x0002
	EjectCard   Disposition = 0x0003
)

// Infinite makes GetStatusChange block until a state change or a cancel.
const Infinite = time.Duration(-1)

// ReaderState is one entry of a GetStatusChange query. The caller fills in
// Reader and CurrentState; GetStatusChange writes back EventState and Atr.
type ReaderState struct {
	Reader       string
	CurrentState StateFlag
	EventState   StateFlag
	Atr          []byte
}

// CardStatus is what Status reports about a connected card.
type CardStatus struct {
	Reader         string
	ActiveProtocol Protocol
	Atr            []byte
}

// Context is a PC/SC resource-manager context.
type Context interface {
	// ListReaders returns the names of the readers known to the resource
	// manager. A machine with no reader attached yields an empty list and no
	// error; only a failure of the resource manager itself is an error, which
	// is what callers use to tell that a context has gone stale.
	ListReaders() ([]string, error)

	// GetStatusChange waits for one of the given readers to change state, and
	// writes the outcome back into states. It reports ErrTimeout if nothing
	// changed within timeout; a timeout of 0 polls the current state and
	// returns immediately, and Infinite blocks until Cancel is called.
	GetStatusChange(states []ReaderState, timeout time.Duration) error

	// Connect connects to the card in the named reader.
	Connect(reader string, mode ShareMode, proto Protocol) (Card, error)

	// Cancel unblocks a GetStatusChange call made on this context, which then
	// reports ErrCancelled.
	Cancel() error

	// Release releases the context.
	Release() error
}

// Card is a connection to a card in a reader.
type Card interface {
	// ActiveProtocol reports the protocol negotiated at connect time.
	ActiveProtocol() Protocol

	// Status reports the reader name, protocol and ATR of the card.
	Status() (*CardStatus, error)

	// Transmit sends an APDU and returns the card's response.
	Transmit(cmd []byte) ([]byte, error)

	// Disconnect closes the connection, leaving the card as d says.
	Disconnect(d Disposition) error
}

// Error is a PC/SC status code and the backend's description of it.
type Error struct {
	// Code is the SCARD_* status code, e.g. 0x80100069 for a removed card.
	Code uint32

	msg string
}

func (e *Error) Error() string {
	if e.msg == "" {
		return fmt.Sprintf("scard: status 0x%08X", e.Code)
	}
	return e.msg
}

// Is matches on the status code alone, so that errors.Is works against the
// sentinels below regardless of how a backend words the message.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// The status codes the reader path distinguishes. Compare with errors.Is.
var (
	ErrCancelled     = &Error{Code: 0x80100002} // SCARD_E_CANCELLED
	ErrTimeout       = &Error{Code: 0x8010000A} // SCARD_E_TIMEOUT
	ErrNoSmartcard   = &Error{Code: 0x8010000C} // SCARD_E_NO_SMARTCARD
	ErrUnpoweredCard = &Error{Code: 0x80100067} // SCARD_W_UNPOWERED_CARD
	ErrResetCard     = &Error{Code: 0x80100068} // SCARD_W_RESET_CARD
	ErrRemovedCard   = &Error{Code: 0x80100069} // SCARD_W_REMOVED_CARD
)

// statusNoReadersAvailable is SCARD_E_NO_READERS_AVAILABLE, which both backends
// normalise into an empty reader list.
const statusNoReadersAvailable = 0x8010002E

// statusError pairs a status code with the backend's message. A zero code means
// the call failed before it reached the PC/SC library, so err is passed through
// unchanged and stays comparable to whatever the backend returned.
func statusError(code uint32, err error) error {
	if err == nil {
		return nil
	}
	if code == 0 {
		return err
	}
	return &Error{Code: code, msg: err.Error()}
}
