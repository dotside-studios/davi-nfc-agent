package nfc

import (
	"context"
	"fmt"
)

// MaxSequenceSteps bounds the exchanges one sequence may carry.
const MaxSequenceSteps = 32

// SequenceStep is one command of a sequence and the status words that decide
// whether the run goes on after it. Status words are the reply's last two bytes
// as one value, SW1 high.
type SequenceStep struct {
	Data []byte

	// ExpectSW stops the run after this step unless the reply's status word is
	// one of these. Empty expects anything.
	ExpectSW []uint16

	// StopOnSW stops the run after this step when the reply's status word is one
	// of these.
	StopOnSW []uint16
}

// SequenceResult is what a sequence returned.
type SequenceResult struct {
	// Replies holds the full reply of every step that ran, status word
	// included.
	Replies [][]byte

	// StoppedAt is the index of the step whose reply ended the run early, or -1
	// when every step ran.
	StoppedAt int
}

// Stops reports whether the reply to this step ends the run.
func (s SequenceStep) Stops(reply []byte) bool {
	if len(s.ExpectSW) == 0 && len(s.StopOnSW) == 0 {
		return false
	}
	if len(reply) < 2 {
		return len(s.ExpectSW) > 0
	}
	sw := uint16(reply[len(reply)-2])<<8 | uint16(reply[len(reply)-1])
	for _, stop := range s.StopOnSW {
		if sw == stop {
			return true
		}
	}
	if len(s.ExpectSW) == 0 {
		return false
	}
	for _, ok := range s.ExpectSW {
		if sw == ok {
			return false
		}
	}
	return true
}

// ValidateSequence refuses a sequence the agent will not send: none, too many
// steps, or a step with no command.
func ValidateSequence(steps []SequenceStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("no steps to send")
	}
	if len(steps) > MaxSequenceSteps {
		return fmt.Errorf("a sequence carries at most %d steps, got %d", MaxSequenceSteps, len(steps))
	}
	for i, step := range steps {
		if len(step.Data) == 0 {
			return fmt.Errorf("step %d has no command bytes", i)
		}
	}
	return nil
}

// RunSequence sends each step through exchange until one stops the run. A
// transport error ends it with that error. It is the one place the stop rules
// are applied, so a reader, a phone driven step by step and a check of a
// device's own run all agree.
func RunSequence(steps []SequenceStep, exchange func([]byte) ([]byte, error)) (*SequenceResult, error) {
	result := &SequenceResult{StoppedAt: -1}
	for i, step := range steps {
		reply, err := exchange(step.Data)
		if err != nil {
			return nil, err
		}
		result.Replies = append(result.Replies, reply)
		if step.Stops(reply) {
			result.StoppedAt = i
			break
		}
	}
	return result, nil
}

// TransceiveSequenceExpecting runs steps against the tag expectUID names under
// one tag operation, so nothing else reaches the card between them. See
// TransceiveExpecting for the guard.
func (r *deviceReader) TransceiveSequenceExpecting(ctx context.Context, steps []SequenceStep, expectUID string) (*SequenceResult, error) {
	if err := ValidateSequence(steps); err != nil {
		return nil, err
	}

	var result *SequenceResult
	err := r.withTagOperation(ctx, func() error {
		tag, err := r.soleTag(expectUID)
		if err != nil {
			return err
		}
		if !CanTagTransceive(tag) {
			return NewNotSupportedError("Transceive")
		}
		transceiver, ok := tag.(TagTransceiver)
		if !ok {
			return NewNotSupportedError("Transceive")
		}
		result, err = RunSequence(steps, transceiver.Transceive)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TransceiveSequenceInSession runs steps inside a lease, holding it for the
// whole run. Each reply renews the lease. A card that has left ends it.
func (r *deviceReader) TransceiveSequenceInSession(ctx context.Context, leaseID string, steps []SequenceStep) (*SequenceResult, error) {
	if err := ValidateSequence(steps); err != nil {
		return nil, err
	}
	l, err := r.leaseByID(leaseID)
	if err != nil {
		return nil, err
	}

	l.exec.Lock()
	defer l.exec.Unlock()

	return RunSequence(steps, func(data []byte) ([]byte, error) {
		l.mu.Lock()
		ended := l.ended
		l.mu.Unlock()
		if ended {
			return nil, NewRawSessionExpiredError("RawSession")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := l.tag.(TagTransceiver).Transceive(data)
		if err != nil {
			if IsCardRemovedError(err) || IsTagRemovedError(err) {
				r.endLease(l)
			}
			return nil, err
		}
		l.refresh(r.clock.Now())
		return resp, nil
	})
}

// WithNTAG424 runs fn under one tag operation on the NTAG 424 DNA expectUID
// names, refusing as not supported when the tag present is another kind.
func (r *deviceReader) WithNTAG424(ctx context.Context, expectUID string, fn func(NTAG424Operator) error) error {
	return r.withTagOperation(ctx, func() error {
		tag, err := r.soleTag(expectUID)
		if err != nil {
			return err
		}
		op, ok := tag.(NTAG424Operator)
		if !ok {
			return NewNotSupportedError("NTAG424")
		}
		return fn(op)
	})
}
