// The adapter over the platform's PC/SC library: the calls and status codes
// this package needs, with the backend behind them chosen at build time.
//
// The types here carry the same numeric values as the PC/SC SCARD_* definitions,
// so both backends map onto them by conversion rather than by lookup table.

package pcsc

import (
	"fmt"
	"time"
)

// stateFlag is a reader state bitmask (SCARD_STATE_*). Its upper 16 bits carry
// the reader's event counter, which pcsc-lite increments on every card
// insertion or removal.
type stateFlag uint32

const (
	stateUnaware     stateFlag = 0x0000
	stateIgnore      stateFlag = 0x0001
	stateChanged     stateFlag = 0x0002
	stateUnknown     stateFlag = 0x0004
	stateUnavailable stateFlag = 0x0008
	stateEmpty       stateFlag = 0x0010
	statePresent     stateFlag = 0x0020
	stateAtrmatch    stateFlag = 0x0040
	stateExclusive   stateFlag = 0x0080
	stateInuse       stateFlag = 0x0100
	stateMute        stateFlag = 0x0200
	stateUnpowered   stateFlag = 0x0400
)

// protocol is a card transmission protocol (SCARD_PROTOCOL_*).
type protocol uint32

const (
	protocolUndefined protocol = 0x0000
	protocolT0        protocol = 0x0001
	protocolT1        protocol = 0x0002
	protocolAny       protocol = protocolT0 | protocolT1
)

// shareMode says how the reader is shared with other applications
// (SCARD_SHARE_*).
type shareMode uint32

const (
	shareExclusive shareMode = 0x0001
	shareShared    shareMode = 0x0002
	shareDirect    shareMode = 0x0003
)

// disposition says what to do with the card on disconnect (SCARD_*_CARD).
type disposition uint32

const (
	leaveCard   disposition = 0x0000
	resetCard   disposition = 0x0001
	unpowerCard disposition = 0x0002
	ejectCard   disposition = 0x0003
)

// infiniteTimeout makes GetStatusChange block until a state change or a cancel.
const infiniteTimeout = time.Duration(-1)

// readerState is one entry of a GetStatusChange query. The caller fills in
// Reader and CurrentState; GetStatusChange writes back EventState and Atr.
type readerState struct {
	Reader       string
	CurrentState stateFlag
	EventState   stateFlag
	Atr          []byte
}

// cardStatus is what Status reports about a connected card.
type cardStatus struct {
	Reader         string
	ActiveProtocol protocol
	Atr            []byte
}

// scardContext is a PC/SC resource-manager context.
type scardContext interface {
	// ListReaders returns the names of the readers known to the resource
	// manager. A machine with no reader attached yields an empty list and no
	// error; only a failure of the resource manager itself is an error, which
	// is what callers use to tell that a context has gone stale.
	ListReaders() ([]string, error)

	// GetStatusChange waits for one of the given readers to change state, and
	// writes the outcome back into states. It reports errTimeout if nothing
	// changed within timeout; a timeout of 0 polls the current state and
	// returns immediately, and infiniteTimeout blocks until Cancel is called.
	GetStatusChange(states []readerState, timeout time.Duration) error

	// Connect connects to the card in the named reader.
	Connect(reader string, mode shareMode, proto protocol) (scardCard, error)

	// Cancel unblocks a GetStatusChange call made on this context, which then
	// reports errCancelled.
	Cancel() error

	// Release releases the context.
	Release() error
}

// scardCard is a connection to a card in a reader.
type scardCard interface {
	// ActiveProtocol reports the protocol negotiated at connect time.
	ActiveProtocol() protocol

	// Status reports the reader name, protocol and ATR of the card.
	Status() (*cardStatus, error)

	// Transmit sends an APDU and returns the card's response.
	Transmit(cmd []byte) ([]byte, error)

	// Disconnect closes the connection, leaving the card as d says.
	Disconnect(d disposition) error
}

// Error is a PC/SC status code and the backend's description of it.
type scardError struct {
	// Code is the SCARD_* status code, e.g. 0x80100069 for a removed card.
	Code uint32

	msg string
}

func (e *scardError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	if name, ok := statusNames[e.Code]; ok {
		return fmt.Sprintf("scard: %s (0x%08X)", name, e.Code)
	}
	return fmt.Sprintf("scard: status 0x%08X", e.Code)
}

// statusNames spells the codes this package names itself, for the errors it
// raises without a backend message behind them. Backends differ in which
// helpers they expose per platform, so the adapter carries its own.
var statusNames = map[uint32]string{
	0x80100002: "SCARD_E_CANCELLED",
	0x8010000A: "SCARD_E_TIMEOUT",
	0x8010000C: "SCARD_E_NO_SMARTCARD",
	0x8010001E: "SCARD_E_SERVICE_STOPPED",
	0x8010002E: "SCARD_E_NO_READERS_AVAILABLE",
	0x80100067: "SCARD_W_UNPOWERED_CARD",
	0x80100068: "SCARD_W_RESET_CARD",
	0x80100069: "SCARD_W_REMOVED_CARD",
}

// Is matches on the status code alone, so that errors.Is works against the
// sentinels below regardless of how a backend words the message.
func (e *scardError) Is(target error) bool {
	t, ok := target.(*scardError)
	return ok && t.Code == e.Code
}

// The status codes the reader path distinguishes. Compare with errors.Is.
var (
	errCancelled     = &scardError{Code: 0x80100002} // SCARD_E_CANCELLED
	errTimeout       = &scardError{Code: 0x8010000A} // SCARD_E_TIMEOUT
	errNoSmartcard   = &scardError{Code: 0x8010000C} // SCARD_E_NO_SMARTCARD
	errUnpoweredCard = &scardError{Code: 0x80100067} // SCARD_W_UNPOWERED_CARD
	errResetCard     = &scardError{Code: 0x80100068} // SCARD_W_RESET_CARD
	errRemovedCard   = &scardError{Code: 0x80100069} // SCARD_W_REMOVED_CARD
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
	return &scardError{Code: code, msg: err.Error()}
}
