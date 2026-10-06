package nfc

import (
	"context"
	"strings"
)

// NTAG424Session is the NTAG 424 DNA driver as an operation needs it: the
// card's commands, the key the agent holds for a number, and the lock.
type NTAG424Session interface {
	NTAG424Operator
	NTAG424KeySource
	TagLocker
}

// NewNTAG424Session drives the NTAG 424 DNA on transport with keys, so one
// session implementation serves a reader and a phone alike. The session lives
// as long as the value does: nothing outlives the operation it was made for.
func NewNTAG424Session(transport CardTransport, uid string, keys NTAG424Keys) NTAG424Session {
	tag := newPCSCNTAG424Tag(transport, uid)
	tag.SetNTAG424Keys(keys)
	return tag
}

// NewNTAG424SessionWithBudget is NewNTAG424Session for an operation with a
// deadline: every exchange on transport is timed into budget, and each step that
// changes the card checks the time left before it sends anything (see
// OperationDeadlineError). A nil budget is NewNTAG424Session.
func NewNTAG424SessionWithBudget(transport CardTransport, uid string, keys NTAG424Keys, budget *OpBudget) NTAG424Session {
	tag := newPCSCNTAG424Tag(budget.Wrap(transport), uid)
	tag.SetNTAG424Keys(keys)
	tag.budget = budget
	return tag
}

// TagSessionHolder is what a TagHolder offers when the tag it holds is not
// polled by the agent, so nothing serializes the operations on it. fn runs with
// the tag held for it alone, after checking the tag present is the one named.
type TagSessionHolder interface {
	WithTagSession(ctx context.Context, deviceID, tagUID string, fn func() error) error
}

// TagTyper is what a TagHolder offers when it can name the type its device
// reported for the tag it holds, empty when it reported none.
type TagTyper interface {
	TagTypeOn(deviceID string) string
}

// ntag424Excluded reports whether a device's type string names a card that is
// not an NTAG 424 DNA. A phone often says only that the tag is ISO-DEP or Type
// 4, which is not a refusal; one that names another family is.
func ntag424Excluded(tagType string) bool {
	t := strings.ToLower(tagType)
	if t == "" {
		return false
	}
	for _, other := range []string{"classic", "ultralight", "desfire", "ntag21", "ntag 21", "ntag20", "ntag 20"} {
		if strings.Contains(t, other) {
			return true
		}
	}
	return false
}

// TagSequencer is what a TagHolder offers when it can run a sequence on a tag
// it holds, whether by one request to the device or one request per step.
type TagSequencer interface {
	TransceiveSequenceTag(ctx context.Context, deviceID, tagUID string, steps []SequenceStep) (*SequenceResult, error)
}

// TagTransport presents the tag a holder holds as a CardTransport, so a driver
// written against one runs over the holder's own exchange. Every exchange is
// APDU-level, under ctx.
func TagTransport(ctx context.Context, holder TagHolder, deviceID, tagUID string) CardTransport {
	return &heldTagTransport{ctx: ctx, holder: holder, deviceID: deviceID, tagUID: tagUID}
}

type heldTagTransport struct {
	ctx      context.Context
	holder   TagHolder
	deviceID string
	tagUID   string
}

func (t *heldTagTransport) Transceive(cmd []byte) ([]byte, error) {
	return t.holder.TransceiveTag(t.ctx, t.deviceID, t.tagUID, cmd, false)
}

func (t *heldTagTransport) IsCardPresent() bool { return t.ctx.Err() == nil }
