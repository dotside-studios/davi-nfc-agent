package agent

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The agent answers for every tag it can reach, which is what its client server
// asks of it. It forwards to the supervisor, which holds the readers it opened
// and the driver holding what the devices report.
//
// Asked of the agent rather than of the supervisor directly because the
// supervisor is replaced by every restart, and because a filter the agent
// applies to a scan belongs on the operation for that tag too. See
// [nfc.TagHolder].

var _ nfc.TagHolder = (*Agent)(nil)

// errNotServing is what an operation gets before Start and after Stop. The
// agent answers rather than leaving a caller with a nil to dereference.
var errNotServing = errors.New("the agent is not serving tag operations")

// TagOn reports the tag a device is holding, by UID. An empty device asks for
// whatever is holding one.
func (a *Agent) TagOn(device string) (string, string, bool) {
	readers := a.supervisor.Load()
	if readers == nil {
		return "", "", false
	}
	return readers.TagOn(device)
}

// DevicesHoldingTags lists what has a tag to name, readers first.
func (a *Agent) DevicesHoldingTags() []string {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil
	}
	return readers.DevicesHoldingTags()
}

// WriteTag encodes msg onto the tag the named device is holding, locking it
// afterwards when lock is set.
func (a *Agent) WriteTag(device, tagUID string, msg *nfc.NDEFMessage, lock bool, idempotencyKey string) (*nfc.WriteResult, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	return readers.WriteTag(device, tagUID, msg, lock, idempotencyKey)
}

// LockTag makes the tag the named device is holding permanently read-only.
func (a *Agent) LockTag(device, tagUID, idempotencyKey string) (*nfc.LockResult, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	return readers.LockTag(device, tagUID, idempotencyKey)
}

// TransceiveTag exchanges raw bytes with the tag the named device is holding.
func (a *Agent) TransceiveTag(device, tagUID string, data []byte, raw bool) ([]byte, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	return readers.TransceiveTag(device, tagUID, data, raw)
}

// TagCapabilities reports what the tag the named device is holding supports.
func (a *Agent) TagCapabilities(device, tagUID string) (*nfc.TagCapabilities, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	return readers.TagCapabilities(device, tagUID)
}
