package nfc

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// NTAG424PhoneOperationTimeout is the time one NTAG 424 DNA operation on a tag
// a phone holds may take in all, from the moment the phone is free for it.
//
// Each exchange with a phone is bounded on its own (5 s on the shipped device
// server), which says nothing about the operation. The longest one, configuring
// SDM, is about two dozen exchanges: authentication is a select and two round
// trips, and the NDEF write, the settings change under a second session and the
// read-back each add several more: 22 in the simulated phone's run over a
// factory card. A phone on a LAN answers in 50 to 300 ms, so the operation takes
// a few seconds; 15 s leaves a wide margin for a slow phone, and ends the
// operation before the roughly 20 s after which iOS ends a tag session, which
// would otherwise cut it off mid-write.
const NTAG424PhoneOperationTimeout = 15 * time.Second

// NTAG424ExchangeFloor is the least time the budget assumes one exchange with a
// phone takes, before any has been seen and when all seen were faster.
const NTAG424ExchangeFloor = 100 * time.Millisecond

// OperationDeadlineError reports that a step that changes the tag was not
// started because the time left for the operation would not cover it. Nothing
// of the step was sent. Retryable: the same operation may fit when the phone
// answers faster.
type OperationDeadlineError struct {
	// Step names what was about to be sent, such as "write NDEF data".
	Step string

	// Exchanges is the number of device exchanges the step needs, and
	// PerExchange the time each was estimated to take: the slowest seen in this
	// operation, or the floor.
	Exchanges   int
	PerExchange time.Duration

	// Remaining is what was left of the operation's time.
	Remaining time.Duration
}

func (e *OperationDeadlineError) Error() string {
	return fmt.Sprintf("deadline would be exceeded before %s: %d exchanges at about %s need %s and %s remain; no write sent",
		e.Step, e.Exchanges, e.PerExchange.Round(time.Millisecond),
		(time.Duration(e.Exchanges) * e.PerExchange).Round(time.Millisecond),
		max(e.Remaining, 0).Round(time.Millisecond))
}

// IsOperationDeadlineError reports whether err is, or wraps, an
// OperationDeadlineError.
func IsOperationDeadlineError(err error) bool {
	var target *OperationDeadlineError
	return errors.As(err, &target)
}

// OpBudget is the time left for one operation, and what it has learned of how
// long an exchange takes. It lets a driver refuse to start a step that cannot
// finish, which a deadline on the exchange alone cannot: that fails the step
// after it has begun. A nil budget has no limit.
type OpBudget struct {
	clock    Clock
	deadline time.Time
	floor    time.Duration

	mu      sync.Mutex
	slowest time.Duration
}

// NewOpBudget returns a budget that runs out at deadline on clock. An exchange
// is assumed to take at least floor.
func NewOpBudget(clock Clock, deadline time.Time, floor time.Duration) *OpBudget {
	if clock == nil {
		clock = NewRealClock()
	}
	return &OpBudget{clock: clock, deadline: deadline, floor: floor}
}

// Deadline is the instant the budget runs out.
func (b *OpBudget) Deadline() time.Time { return b.deadline }

// Remaining is the time left.
func (b *OpBudget) Remaining() time.Duration {
	return b.deadline.Sub(b.clock.Now())
}

// Observe records that an exchange took d.
func (b *OpBudget) Observe(d time.Duration) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.slowest = max(b.slowest, d)
	b.mu.Unlock()
}

// PerExchange is the time one exchange is estimated to take: the slowest seen,
// and never less than the floor.
func (b *OpBudget) PerExchange() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return max(b.slowest, b.floor)
}

// Require checks that step, which needs exchanges device exchanges, fits in
// what is left. It returns an OperationDeadlineError when it does not.
func (b *OpBudget) Require(step string, exchanges int) error {
	if b == nil {
		return nil
	}
	per := b.PerExchange()
	remaining := b.Remaining()
	if time.Duration(exchanges)*per > remaining {
		return &OperationDeadlineError{Step: step, Exchanges: exchanges, PerExchange: per, Remaining: remaining}
	}
	return nil
}

// Wrap returns transport with every exchange timed into the budget.
func (b *OpBudget) Wrap(transport CardTransport) CardTransport {
	if b == nil {
		return transport
	}
	return &timedTransport{CardTransport: transport, budget: b}
}

type timedTransport struct {
	CardTransport
	budget *OpBudget
}

func (t *timedTransport) Transceive(cmd []byte) ([]byte, error) {
	start := t.budget.clock.Now()
	resp, err := t.CardTransport.Transceive(cmd)
	t.budget.Observe(t.budget.clock.Now().Sub(start))
	return resp, err
}

// SDMState says what a failed ConfigureSDM left on the tag.
type SDMState string

const (
	// SDMNothingWritten: the failure came before any write was sent.
	SDMNothingWritten SDMState = "nothing written"

	// SDMNDEFWritten: the NDEF message was written and the file settings were
	// not changed, so the tag holds the new message with SDM not configured.
	SDMNDEFWritten SDMState = "NDEF written, SDM not configured"

	// SDMNDEFIndeterminate: a write of the NDEF file failed after it was sent,
	// so the file may hold part of the new message.
	SDMNDEFIndeterminate SDMState = "NDEF write interrupted, file contents indeterminate"

	// SDMSettingsIndeterminate: the file settings change failed after it was
	// sent, so SDM may or may not be configured. The NDEF message is written.
	SDMSettingsIndeterminate SDMState = "NDEF written, SDM configuration unconfirmed"

	// SDMConfiguredUnverified: the NDEF message is written and SDM configured,
	// and the read-back that verifies them did not succeed.
	SDMConfiguredUnverified SDMState = "NDEF written, SDM configured, read-back not verified"
)

// SDMStateError wraps the failure of ConfigureSDM with what it left on the tag.
type SDMStateError struct {
	State SDMState
	Err   error
}

func (e *SDMStateError) Error() string { return fmt.Sprintf("%s (tag state: %s)", e.Err, e.State) }

func (e *SDMStateError) Unwrap() error { return e.Err }

// SDMStateOf returns the state a ConfigureSDM failure left, and false when err
// carries none.
func SDMStateOf(err error) (SDMState, bool) {
	var target *SDMStateError
	if errors.As(err, &target) {
		return target.State, true
	}
	return "", false
}
