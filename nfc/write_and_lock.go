package nfc

import "fmt"

// AtomicLockWriter is an optional interface for a tag that can be written and
// locked in one operation.
//
// Writing then locking is two steps, and a failure between them leaves a tag
// carrying data that was meant to be permanent but is not. A tag reached over a
// connection that offers both in one exchange implements this so the pair
// cannot come apart; one that cannot is written and locked in sequence, which
// is all a local tag can do anyway.
type AtomicLockWriter interface {
	WriteDataAndLock(data []byte) error
}

// WriteMessage encodes msg onto the card, confirming and locking it as far as
// the tag allows.
//
// The steps are the same wherever the tag is. What differs is what the tag can
// support, which it declares: capacity is checked when it reports one,
// verification is skipped when its reads are a snapshot, and a lock is folded
// into the write when it can be.
func WriteMessage(card *Card, msg *NDEFMessage, opts WriteOptions, clock Clock) (*WriteResult, error) {
	if card == nil {
		return nil, fmt.Errorf("WriteMessage: no card")
	}
	if clock == nil {
		clock = &RealClock{}
	}

	result, err := writeMessageToCard(card, msg, opts, clock)
	if err != nil {
		return nil, err
	}

	// Already locked when the write carried it.
	if opts.Lock && !result.Locked {
		if _, err := lockCard(card); err != nil {
			return nil, fmt.Errorf("write to card UID %s succeeded but lock failed: %w", card.UID, err)
		}
		result.Locked = true
	}

	return result, nil
}
