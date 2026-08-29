package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The agent answers for every tag it can reach, which is what its client server
// asks of it. It forwards to the supervisor, which holds the readers it opened
// and the driver holding what the devices report.
//
// Asked of the agent rather than of the supervisor directly because the
// supervisor is replaced by every restart, and because a filter the agent
// applies to a scan belongs on the operation for that tag too: the pin decides
// which readers this agent works with, so what a client can see is what it can
// reach. See [nfc.TagHolder] and [Agent.pinAdmits].

var _ nfc.TagHolder = (*Agent)(nil)

// errNotServing is what an operation gets before Start and after Stop. The
// agent answers rather than leaving a caller with a nil to dereference.
var errNotServing = errors.New("the agent is not serving tag operations")

// TagOn reports the tag a device is holding, by UID. An empty device asks for
// whatever is holding one, which is whatever the pin admits.
//
// A reader the pin excludes reports nothing, which is the truth from the
// caller's side: it was never shown that reader's scans either.
func (a *Agent) TagOn(device string) (string, string, bool) {
	readers := a.supervisor.Load()
	if readers == nil {
		return "", "", false
	}

	if device != "" {
		if !a.pinAdmits(device) {
			return "", "", false
		}
		return readers.TagOn(device)
	}

	// Nothing named, so the pick is the holder's own preference over what the
	// pin admits: a source whose tag can be named beats one holding a tag it
	// could not read, and readers come before devices in the list.
	unnamed := ""
	for _, holding := range a.DevicesHoldingTags() {
		holder, uid, ok := readers.TagOn(holding)
		if !ok {
			continue
		}
		if uid != "" {
			return holder, uid, true
		}
		if unnamed == "" {
			unnamed = holder
		}
	}
	if unnamed != "" {
		return unnamed, "", true
	}
	return "", "", false
}

// DevicesHoldingTags lists what has a tag to name, readers first, leaving out
// the readers the pin excludes.
func (a *Agent) DevicesHoldingTags() []string {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil
	}

	holding := readers.DevicesHoldingTags()
	admitted := make([]string, 0, len(holding))
	for _, device := range holding {
		if a.pinAdmits(device) {
			admitted = append(admitted, device)
		}
	}
	return admitted
}

// operateOn is the device an operation applies to: the one it named, refused
// when the pin excludes it, or whatever the pin admits when it named none.
func (a *Agent) operateOn(device string) (string, error) {
	if device != "" {
		if !a.pinAdmits(device) {
			return "", fmt.Errorf("%s is not the reader this agent is set to use", device)
		}
		return device, nil
	}

	holding, _, ok := a.TagOn("")
	if !ok {
		return "", errors.New("no reader this agent is set to use is holding a tag")
	}
	return holding, nil
}

// WriteTag encodes msg onto the tag the named device is holding, locking it
// afterwards when lock is set.
func (a *Agent) WriteTag(ctx context.Context, device, tagUID string, msg *nfc.NDEFMessage, lock bool, idempotencyKey string) (*nfc.WriteResult, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	device, err := a.operateOn(device)
	if err != nil {
		return nil, err
	}
	return readers.WriteTag(ctx, device, tagUID, msg, lock, idempotencyKey)
}

// LockTag makes the tag the named device is holding permanently read-only.
func (a *Agent) LockTag(ctx context.Context, device, tagUID, idempotencyKey string) (*nfc.LockResult, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	device, err := a.operateOn(device)
	if err != nil {
		return nil, err
	}
	return readers.LockTag(ctx, device, tagUID, idempotencyKey)
}

// TransceiveTag exchanges raw bytes with the tag the named device is holding.
func (a *Agent) TransceiveTag(ctx context.Context, device, tagUID string, data []byte, raw bool) ([]byte, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	device, err := a.operateOn(device)
	if err != nil {
		return nil, err
	}
	return readers.TransceiveTag(ctx, device, tagUID, data, raw)
}

// TagCapabilities reports what the tag the named device is holding supports.
func (a *Agent) TagCapabilities(ctx context.Context, device, tagUID string) (*nfc.TagCapabilities, error) {
	readers := a.supervisor.Load()
	if readers == nil {
		return nil, errNotServing
	}
	device, err := a.operateOn(device)
	if err != nil {
		return nil, err
	}
	return readers.TagCapabilities(ctx, device, tagUID)
}
