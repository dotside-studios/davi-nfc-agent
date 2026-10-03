package remotenfc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/virtualnfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

var (
	_ nfc.TagSessionHolder = (*Manager)(nil)
	_ nfc.TagSequencer     = (*Manager)(nil)
	_ nfc.TagTyper         = (*Manager)(nil)
)

// TagTypeOn is the type the device reported for the tag it is holding, empty
// when it holds none or reported none.
func (m *Manager) TagTypeOn(deviceID string) string {
	return tagTypeOf(m.tagOn(deviceID))
}

// WithTagSession runs fn with the tag the device is holding reserved for it
// alone, so another operation on the same device cannot interleave its
// exchanges with fn's. It refuses when the device holds no tag, holds another
// one than tagUID names, or did not declare that it exchanges APDUs.
//
// Reading the tag's state is not gated by the agent's mode here: whatever fn
// sends that could change the tag is gated where the operation is accepted,
// which is also where a reader's equivalent reads are let through.
func (m *Manager) WithTagSession(ctx context.Context, deviceID, tagUID string, fn func() error) error {
	info, ok := m.ActiveTag(deviceID)
	if !ok {
		return protocol.Errorf(protocol.ErrCodeNoCard, "device %s is not holding a tag", deviceID)
	}
	deviceID = info.DeviceID

	if tagUID != "" && !sameUID(tagUID, info.UID) {
		return &nfc.NFCError{
			Code:    nfc.ErrCodeTagRemoved,
			Op:      "TagSession",
			TagUID:  tagUID,
			Message: fmt.Sprintf("device %s is holding %s, not %s", deviceID, info.UID, tagUID),
		}
	}
	if !m.canExchange(deviceID, info.Tag) {
		return nfc.NewNotSupportedError("Transceive")
	}

	device, ok := m.GetDevice(deviceID)
	if !ok {
		return protocol.Errorf(protocol.ErrCodeNoCard, "device %s is not connected", deviceID)
	}
	select {
	case device.opSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-device.opSlot }()

	return fn()
}

// canExchange reports whether the device and the tag it holds both allow an
// APDU exchange, setting aside the agent's mode (see WithTagSession).
func (m *Manager) canExchange(deviceID string, held nfc.Tag) bool {
	var declared *protocol.TagCapabilities
	if tag, ok := held.(*Tag); ok {
		tag.mu.RLock()
		declared = tag.declaredCaps
		tag.mu.RUnlock()
	}
	return virtualnfc.TransceiveAllowed(declared, exchangeSource{m: m, deviceID: deviceID})
}

// exchangeSource is a device's bound on raw exchange alone, for the capability
// merge, without the mode that withdraws every operation that can write.
type exchangeSource struct {
	m        *Manager
	deviceID string
}

func (s exchangeSource) CanWrite() bool { return false }
func (s exchangeSource) CanLock() bool  { return false }
func (s exchangeSource) CanTransceive() bool {
	return s.m.deviceDeclaredAs(s.deviceID, false, func(c DeviceCapabilities) bool { return c.CanTransceive })
}

// deviceCanSequence reports whether the device declared that it runs a batch
// itself. A device that said nothing did not.
func (m *Manager) deviceCanSequence(deviceID string) bool {
	device, ok := m.GetDevice(deviceID)
	if !ok {
		return false
	}
	caps, declared := device.DeclaredCapabilities()
	return declared && caps.CanTransceive && caps.CanTransceiveSequence
}

// TransceiveSequenceTag runs the steps against the tag the device is holding as
// one tag operation. A device that declared canTransceiveSequence is sent one
// request and answers with every reply; any other is sent one exchange per step
// under the same stop rules, which is what the single request is for avoiding.
func (m *Manager) TransceiveSequenceTag(ctx context.Context, deviceID, tagUID string, steps []nfc.SequenceStep) (*nfc.SequenceResult, error) {
	if err := nfc.ValidateSequence(steps); err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "invalid sequence")
	}

	var result *nfc.SequenceResult
	err := m.WithTagSession(ctx, deviceID, tagUID, func() error {
		info, _ := m.ActiveTag(deviceID)
		var err error
		if m.deviceCanSequence(info.DeviceID) {
			result, err = m.deviceSequence(ctx, info.DeviceID, tagUID, steps)
		} else {
			result, err = nfc.RunSequence(steps, func(data []byte) ([]byte, error) {
				return m.TransceiveTag(ctx, info.DeviceID, tagUID, data, false)
			})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// deviceSequence sends the whole batch to the device and checks what comes back
// against the rules the agent sent: a device that stopped early, or ran on past
// a stop, is reported rather than believed.
func (m *Manager) deviceSequence(ctx context.Context, deviceID, tagUID string, steps []nfc.SequenceStep) (*nfc.SequenceResult, error) {
	wire := make([]DeviceSequenceStep, len(steps))
	for i, step := range steps {
		wire[i] = DeviceSequenceStep{Data: step.Data, ExpectSW: formatSWs(step.ExpectSW), StopOnSW: formatSWs(step.StopOnSW)}
	}

	resp, err := m.TransceiveSequenceWithDevice(ctx, deviceID, DeviceTransceiveSequenceRequest{
		RequestID: m.nextRequestID("sequence"),
		DeviceID:  deviceID,
		TagUID:    tagUID,
		Steps:     wire,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		message := resp.Error
		if message == "" {
			message = "device reported the sequence failed"
		}
		return nil, &nfc.NFCError{
			Code:    protocol.InternalErrorCode(resp.ErrorCode, nfc.ErrCodeTransceiveFailed),
			Op:      "TransceiveSequence",
			TagUID:  tagUID,
			Message: message,
		}
	}

	return checkDeviceSequence(steps, resp)
}

// checkDeviceSequence accepts a device's run only if it is the run the stop
// rules describe for the replies it returned.
func checkDeviceSequence(steps []nfc.SequenceStep, resp DeviceTransceiveSequenceResponse) (*nfc.SequenceResult, error) {
	bad := func(format string, args ...any) error {
		return &nfc.NFCError{
			Code:    nfc.ErrCodeTransceiveFailed,
			Op:      "TransceiveSequence",
			Message: "device answered the sequence inconsistently: " + fmt.Sprintf(format, args...),
		}
	}

	if len(resp.Replies) == 0 || len(resp.Replies) > len(steps) {
		return nil, bad("%d replies for %d steps", len(resp.Replies), len(steps))
	}

	next := 0
	expected, _ := nfc.RunSequence(steps, func([]byte) ([]byte, error) {
		reply := resp.Replies[min(next, len(resp.Replies)-1)]
		next++
		return reply, nil
	})
	if next > len(resp.Replies) || len(expected.Replies) != len(resp.Replies) {
		return nil, bad("%d replies where the stop rules end the run after %d", len(resp.Replies), len(expected.Replies))
	}
	if expected.StoppedAt != resp.StoppedAt {
		return nil, bad("stoppedAt %d where the stop rules give %d", resp.StoppedAt, expected.StoppedAt)
	}
	return &nfc.SequenceResult{Replies: resp.Replies, StoppedAt: resp.StoppedAt}, nil
}

// TransceiveSequenceWithDevice sends a batch to the tag a device is holding.
func (m *Manager) TransceiveSequenceWithDevice(ctx context.Context, deviceID string, req DeviceTransceiveSequenceRequest) (DeviceTransceiveSequenceResponse, error) {
	req.DeviceID = deviceID
	if req.TimeoutMS == 0 {
		req.TimeoutMS = int(DeviceTransceiveTimeout / time.Millisecond)
	}

	// Each step may take as long as one exchange, and the device is allowed a
	// little longer than it was told to take.
	timeout := time.Duration(req.TimeoutMS)*time.Millisecond*time.Duration(max(len(req.Steps), 1)) + time.Second

	payload, err := m.request(ctx, deviceID, req.RequestID, WSTypeDeviceTransceiveSequenceRequest, req, timeout)
	if err != nil {
		return DeviceTransceiveSequenceResponse{}, err
	}

	var resp DeviceTransceiveSequenceResponse
	if err := decodePayload(payload, &resp); err != nil {
		return DeviceTransceiveSequenceResponse{}, fmt.Errorf("failed to parse sequence response: %w", err)
	}
	return resp, nil
}

// formatSWs writes status words as the four hex characters the device protocol
// carries.
func formatSWs(sws []uint16) []string {
	if len(sws) == 0 {
		return nil
	}
	out := make([]string, len(sws))
	for i, sw := range sws {
		out[i] = strings.ToUpper(fmt.Sprintf("%04x", sw))
	}
	return out
}

// sameUID compares two UIDs as the agent names them: a hex serial in any
// separator and case is one value, anything else must match as it was sent.
func sameUID(a, b string) bool {
	if na, err := ParseUID(a); err == nil {
		a = na
	}
	if nb, err := ParseUID(b); err == nil {
		b = nb
	}
	return strings.EqualFold(a, b)
}
