// Package tagrouter answers one question for every client request: which of the
// agent's tag sources does this apply to, the hardware reader or a paired
// device? It reads the request channels of the bridge and performs the
// operation on whichever source holds the tag the request names.
//
// It serves no HTTP. The device protocol belongs to nfc/remotenfc and the
// listener to server/unifiedserver; this is the part that has to see both a
// reader and a device driver at once, which is why it is neither of them.
package tagrouter

import (
	"context"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Config names the tag sources to route between.
type Config struct {
	// Reader is the agent's own hardware reader. Nil when it has none.
	Reader *nfc.NFCReader

	// Remote is the driver serving paired devices. Nil when none are
	// configured.
	Remote *remotenfc.Manager
}

// Router routes client requests to a tag source.
type Router struct {
	config Config
	bridge *server.ServerBridge
	remote *remotenfc.Manager

	ctx    context.Context
	cancel context.CancelFunc
}

// New builds the router. It reads nothing until Start.
func New(config Config, bridge *server.ServerBridge) *Router {
	return &Router{config: config, bridge: bridge, remote: config.Remote}
}

// Start begins draining the bridge's request channels. It returns once the
// goroutines are running.
func (s *Router) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)

	go s.handleWriteRequests()
	go s.handleLockRequests()
	go s.handleTransceiveRequests()
	go s.handleCapabilitiesRequests()
}

// Stop ends them.
func (s *Router) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// handleWriteRequests listens for write requests from the client server.
func (s *Router) handleWriteRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.WriteRequest:
			if !ok {
				return
			}
			s.executeWriteRequest(msg)
		}
	}
}

// executeWriteRequest executes a write request from the client server.
//
// The hardware reader keeps priority while it actually holds a card, so mixed
// setups behave as they always have. Otherwise the write goes to whichever
// remote device is currently holding a tag.
func (s *Router) executeWriteRequest(msg server.WriteRequestMessage) {
	reader := s.config.Reader

	// The mode gates every route to a tag, not just the hardware one. The
	// reader enforces it inside prepareCardForWrite, which a write routed to a
	// device never reaches.
	if !modeAllowsTagModification(reader) {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     readOnlyModeMessage("writes"),
			ErrorCode: protocol.ErrCodeReadOnly,
		}
		return
	}

	rt, err := s.resolveRoute(msg.TagUID, msg.TargetDevice, msg.AllowUntargeted)
	if err != nil {
		code, text := routeFailure(err)
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     text,
			ErrorCode: code,
		}
		return
	}

	if !rt.reader {
		s.writeViaDevice(msg, rt.device)
		return
	}

	// Build NDEF message
	ndefMsg, err := server.BuildNDEFMessage(msg.Request)
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: protocol.ErrCodeInvalidRequest,
		}
		return
	}

	// Write to card with overwrite option. WriteMessageWithResult performs a
	// capacity check, retries on transient failures, and verifies the write by
	// reading it back, returning the verified outcome.
	result, err := reader.WriteMessageWithResult(ndefMsg, nfc.WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      msg.Request.Lock,
		ExpectUID: msg.TagUID,
	})
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeWriteFailed),
		}
		return
	}

	msg.ResponseCh <- server.WriteResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   result,
	}
}

// writeViaDevice routes a write to the remote device holding a tag. It reports
// whether it handled the request; false means no device was available and the
// caller should fall back to the hardware reader.
func (s *Router) writeViaDevice(msg server.WriteRequestMessage, active remotenfc.ActiveTagInfo) {
	deviceID, uid := active.DeviceID, active.UID

	// Encode here so the device receives exactly the message the hardware path
	// would have written, rather than re-deriving it from records.
	ndefMsg, err := server.BuildNDEFMessage(msg.Request)
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return
	}

	ndefBytes, err := ndefMsg.Encode()
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return
	}

	resp, err := s.remote.WriteToDevice(deviceID, protocol.DeviceWriteRequest{
		RequestID:      msg.RequestID,
		TagUID:         uid,
		NDEFMessage:    server.BuildNDEFInput(msg.Request),
		NDEFBytes:      ndefBytes,
		Lock:           msg.Request.Lock,
		IdempotencyKey: msg.IdempotencyKey,
	})
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return
	}

	msg.ResponseCh <- server.WriteResponseMessage{
		RequestID: msg.RequestID,
		Success:   resp.Success,
		Error:     resp.Error,
		ErrorCode: resp.ErrorCode,
		Payload:   map[string]any{"uid": uid, "deviceID": deviceID},
	}
}

// handleLockRequests listens for make-read-only requests from the client server.
func (s *Router) handleLockRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.LockRequest:
			if !ok {
				return
			}
			s.executeLockRequest(msg)
		}
	}
}

// executeLockRequest executes a lock request from the client server.
//
// Routing mirrors a write: the hardware reader keeps priority while it holds a
// card, otherwise the lock goes to the remote device holding a tag. Without the
// fallback, a lock aimed at a phone-held tag lands on whatever card is sitting
// on the reader, irreversibly.
func (s *Router) executeLockRequest(msg server.LockRequestMessage) {
	reader := s.config.Reader

	if !modeAllowsTagModification(reader) {
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     readOnlyModeMessage("locks"),
			ErrorCode: protocol.ErrCodeReadOnly,
		}
		return
	}

	rt, err := s.resolveRoute(msg.TagUID, msg.TargetDevice, msg.AllowUntargeted)
	if err != nil {
		code, text := routeFailure(err)
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     text,
			ErrorCode: code,
		}
		return
	}

	if !rt.reader {
		s.lockViaDevice(msg, rt.device)
		return
	}

	result, err := reader.LockCardExpecting(msg.TagUID)
	if err != nil {
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeLockFailed),
		}
		return
	}

	msg.ResponseCh <- server.LockResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   result,
	}
}

// lockViaDevice routes a lock to the remote device holding a tag. It reports
// whether it handled the request; false means no device was available and the
// caller should fall back to the hardware reader.
//
// A lock travels as a write request with Lock set and no NDEF. The device
// protocol has one tag-modifying frame, not two.
func (s *Router) lockViaDevice(msg server.LockRequestMessage, active remotenfc.ActiveTagInfo) {
	deviceID, uid := active.DeviceID, active.UID

	resp, err := s.remote.WriteToDevice(deviceID, protocol.DeviceWriteRequest{
		RequestID:      msg.RequestID,
		TagUID:         uid,
		Lock:           true,
		IdempotencyKey: msg.IdempotencyKey,
	})
	if err != nil {
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return
	}
	if !resp.Success {
		code := resp.ErrorCode
		if code == "" {
			code = protocol.ErrCodeLockFailed
		}
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     resp.Error,
			ErrorCode: code,
		}
		return
	}

	msg.ResponseCh <- server.LockResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		// The device reports the outcome but not the tag type.
		Payload: &nfc.LockResult{UID: uid, Locked: true},
	}
}

// handleCapabilitiesRequests listens for capabilities queries from the client server.
func (s *Router) handleTransceiveRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.Transceive:
			if !ok {
				return
			}
			s.executeTransceiveRequest(msg)
		}
	}
}

// executeTransceiveRequest exchanges raw bytes with whichever holds the tag,
// preferring a remote device when the hardware reader has no card. This is the
// routing a write uses, so an APDU reaches the tag the operator is looking at.
func (s *Router) executeTransceiveRequest(msg server.TransceiveRequestMessage) {
	reader := s.config.Reader

	// A raw exchange cannot be assumed harmless: the same call carries a SELECT
	// and a write to a configuration page, so the mode treats it as a write.
	if !modeAllowsTagModification(reader) {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "Reader is in read-only mode; raw exchanges are refused because they can write",
			ErrorCode: protocol.ErrCodeReadOnly,
		}
		return
	}

	rt, err := s.resolveRoute(msg.TagUID, msg.TargetDevice, msg.AllowUntargeted)
	if err != nil {
		code, text := routeFailure(err)
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     text,
			ErrorCode: code,
		}
		return
	}

	if !rt.reader {
		s.transceiveViaDevice(msg, rt.device)
		return
	}

	resp, err := reader.TransceiveExpecting(msg.Data, msg.TagUID)
	if err != nil {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeTransceiveFailed),
		}
		return
	}

	msg.ResponseCh <- server.TransceiveResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Data:      resp,
	}
}

// transceiveViaDevice routes an exchange to the remote device holding a tag. It
// reports whether it handled the request; false means no device was available
// and the caller should fall back to the hardware reader.
func (s *Router) transceiveViaDevice(msg server.TransceiveRequestMessage, active remotenfc.ActiveTagInfo) {
	deviceID, uid := active.DeviceID, active.UID

	resp, err := s.remote.TransceiveWithDevice(deviceID, protocol.DeviceTransceiveRequest{
		RequestID: msg.RequestID,
		DeviceID:  deviceID,
		TagUID:    uid,
		Data:      msg.Data,
		Raw:       msg.Raw,
	})
	if err != nil {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: protocol.ErrCodeTransceiveFailed,
		}
		return
	}
	if !resp.Success {
		code := resp.ErrorCode
		if code == "" {
			code = protocol.ErrCodeTransceiveFailed
		}
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     resp.Error,
			ErrorCode: code,
		}
		return
	}

	msg.ResponseCh <- server.TransceiveResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Data:      resp.Data,
	}
}

func (s *Router) handleCapabilitiesRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.CapabilitiesRequest:
			if !ok {
				return
			}
			s.executeCapabilitiesRequest(msg)
		}
	}
}

// executeCapabilitiesRequest queries the present tag's capabilities.
func (s *Router) executeCapabilitiesRequest(msg server.CapabilitiesRequestMessage) {
	reader := s.config.Reader

	rt, err := s.resolveRoute(msg.TagUID, msg.TargetDevice, msg.AllowUntargeted)
	if err != nil {
		code, text := routeFailure(err)
		msg.ResponseCh <- server.CapabilitiesResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     text,
			ErrorCode: code,
		}
		return
	}

	if !rt.reader {
		s.capabilitiesViaDevice(msg, rt.device)
		return
	}

	if reader == nil || msg.TargetDevice != "" {
		msg.ResponseCh <- server.CapabilitiesResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader or device holding a tag",
			ErrorCode: protocol.ErrCodeNoCard,
		}
		return
	}

	caps, err := reader.GetCapabilities()
	if err != nil {
		msg.ResponseCh <- server.CapabilitiesResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeCapabilitiesFailed),
		}
		return
	}

	msg.ResponseCh <- server.CapabilitiesResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   caps,
	}
}

// capabilitiesViaDevice answers from the tag a remote device is holding. It
// reports whether it handled the request; false means no device was available
// and the caller should fall back to the hardware reader.
//
// No round trip: the device declares a tag's capabilities when it reports the
// scan, and the tag recomputes what the agent can actually route each time it
// is asked. Asking the phone again would only fetch what it already sent.
func (s *Router) capabilitiesViaDevice(msg server.CapabilitiesRequestMessage, active remotenfc.ActiveTagInfo) {
	caps := nfc.GetTagCapabilities(active.Tag)

	msg.ResponseCh <- server.CapabilitiesResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   &caps,
	}
}

// targetDevice resolves which remote device a request is for. A request naming
// one is answered by that device or not at all; naming none falls back to the
// most recent scan.
func (s *Router) targetDevice(target string) (remotenfc.ActiveTagInfo, bool) {
	if s.remote == nil {
		return remotenfc.ActiveTagInfo{}, false
	}
	return s.remote.ActiveTag(target)
}

// modeAllowsTagModification reports whether the agent's current mode permits a
// write, a lock or a raw exchange.
//
// The mode belongs to the agent rather than to the reader, so it governs tags
// held by remote devices too. A nil reader has no mode to consult.
func modeAllowsTagModification(reader *nfc.NFCReader) bool {
	return reader == nil || reader.GetMode() != nfc.ModeReadOnly
}

// readOnlyModeMessage explains a mode refusal for the named operation.
func readOnlyModeMessage(operations string) string {
	return fmt.Sprintf("Agent is in read-only mode; %s are refused", operations)
}

// operationErrorCode classifies a reader failure, falling back to the
// operation's own label when the error carries no code of its own.
func operationErrorCode(err error, fallback protocol.ErrorCode) protocol.ErrorCode {
	if payload := protocol.ErrorPayloadFor(err); payload.Code != protocol.ErrCodeUnknownError {
		return payload.Code
	}
	return fallback
}
