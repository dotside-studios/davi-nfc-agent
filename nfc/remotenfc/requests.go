package remotenfc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/wsconn"
	"github.com/google/uuid"
)

// DeviceWriteTimeout bounds how long the agent waits for a write outcome. A tag
// is only in the field while the user holds it there, so waiting much longer
// reports a stale result.
const DeviceWriteTimeout = 20 * time.Second

// DeviceTransceiveTimeout bounds a single raw exchange. It is far shorter than
// a write because a transceive is one round trip, and a sequence of them has to
// fit inside the time a user holds a tag against the device.
const DeviceTransceiveTimeout = 5 * time.Second

// pendingRequest is an in-flight request awaiting a device's response. The
// payload is untyped so writes and transceives share one waiter.
type pendingRequest struct {
	deviceID string
	respCh   chan pendingResult
}

type pendingResult struct {
	payload map[string]any
	err     error
}

// activeTag is a tag a device is holding in its field. The tag itself is kept,
// not just its UID, so its capabilities can be answered without waiting for the
// next scan.
type activeTag struct {
	deviceID string
	uid      string
	tag      nfc.Tag
}

// ActiveTag returns the tag on the named device. An empty deviceID asks for the
// most recent scan across all devices.
//
// Tags are tracked per device rather than in a single slot: two phones can each
// be holding one, and a request that names its target must reach that target
// rather than whichever scanned last.
func (m *Manager) ActiveTag(deviceID string) (ActiveTagInfo, bool) {
	m.activeMu.RLock()
	defer m.activeMu.RUnlock()

	if deviceID == "" {
		deviceID = m.activeLatest
	}
	if deviceID == "" {
		return ActiveTagInfo{}, false
	}

	active, ok := m.active[deviceID]
	if !ok {
		return ActiveTagInfo{}, false
	}
	return ActiveTagInfo{DeviceID: active.deviceID, UID: active.uid, Tag: active.tag}, true
}

// ActiveTagInfo describes a tag a device is holding.
type ActiveTagInfo struct {
	DeviceID string
	UID      string

	// Tag answers for the tag's capabilities. Nil if the scan carried none.
	Tag nfc.Tag
}

// ActiveTagDevices lists the devices currently holding a tag, most recent first.
func (m *Manager) ActiveTagDevices() []string {
	m.activeMu.RLock()
	defer m.activeMu.RUnlock()

	ids := make([]string, 0, len(m.active))
	if m.activeLatest != "" {
		ids = append(ids, m.activeLatest)
	}
	for deviceID := range m.active {
		if deviceID != m.activeLatest {
			ids = append(ids, deviceID)
		}
	}
	return ids
}

func (m *Manager) setActiveTag(deviceID, uid string, tag nfc.Tag) {
	m.activeMu.Lock()
	defer m.activeMu.Unlock()

	m.active[deviceID] = activeTag{deviceID: deviceID, uid: uid, tag: tag}
	m.activeLatest = deviceID
}

// clearActiveTag forgets a device's tag, but only if it is still the one being
// cleared: the device may have presented another in the meantime.
func (m *Manager) clearActiveTag(deviceID, uid string) {
	m.activeMu.Lock()
	defer m.activeMu.Unlock()

	active, ok := m.active[deviceID]
	if !ok {
		return
	}
	if uid != "" && active.uid != "" && active.uid != uid {
		return
	}

	delete(m.active, deviceID)

	if m.activeLatest == deviceID {
		m.activeLatest = ""
		// Fall back to any other device still holding one, so a second phone
		// keeps working when the first withdraws its tag.
		for id := range m.active {
			m.activeLatest = id
			break
		}
	}
}

func (m *Manager) addSession(deviceID string, conn *wsconn.SafeConn) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()

	m.sessions[deviceID] = conn
	m.sessionConn[conn] = deviceID
}

func (m *Manager) removeSession(deviceID string) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()

	if conn, ok := m.sessions[deviceID]; ok {
		delete(m.sessionConn, conn)
		delete(m.sessions, deviceID)
	}
}

func (m *Manager) deviceIDForConn(conn *wsconn.SafeConn) string {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return m.sessionConn[conn]
}

func (m *Manager) session(deviceID string) (*wsconn.SafeConn, bool) {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()

	conn, ok := m.sessions[deviceID]
	return conn, ok
}

// closeSession drops a device's connection, which runs the ordinary disconnect
// path. It reports whether there was one to close.
func (m *Manager) closeSession(deviceID string) bool {
	conn, ok := m.session(deviceID)
	if !ok {
		return false
	}
	_ = conn.Close()
	return true
}

// SendToDevice sends a message over a device's session.
func (m *Manager) SendToDevice(deviceID string, message any) error {
	conn, ok := m.session(deviceID)
	if !ok {
		return fmt.Errorf("device not connected: %s", deviceID)
	}
	return conn.WriteJSON(message)
}

// request sends a message to a device and waits for the matching response.
// Writes and transceives share this path.
func (m *Manager) request(deviceID, requestID, msgType string, payload any, timeout time.Duration) (map[string]any, error) {
	respCh := make(chan pendingResult, 1)

	m.pendingMu.Lock()
	m.pending[requestID] = pendingRequest{deviceID: deviceID, respCh: respCh}
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pending, requestID)
		m.pendingMu.Unlock()
	}()

	if err := m.SendToDevice(deviceID, protocol.WebSocketMessage{
		ID:      requestID,
		Type:    msgType,
		Payload: payload,
	}); err != nil {
		return nil, err
	}

	select {
	case result := <-respCh:
		return result.payload, result.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("device %s did not respond within %s", deviceID, timeout)
	}
}

// WriteToDevice asks a device to write the tag it holds and waits for the
// outcome. A request with Lock set and no message is a lock.
func (m *Manager) WriteToDevice(deviceID string, req DeviceWriteRequest) (DeviceWriteResponse, error) {
	req.DeviceID = deviceID

	payload, err := m.request(deviceID, req.RequestID, WSTypeDeviceWriteRequest, req, DeviceWriteTimeout)
	if err != nil {
		return DeviceWriteResponse{}, err
	}

	var resp DeviceWriteResponse
	if err := decodePayload(payload, &resp); err != nil {
		return DeviceWriteResponse{}, fmt.Errorf("failed to parse write response: %w", err)
	}
	return resp, nil
}

// TransceiveWithDevice sends a raw exchange to the tag a device is holding.
func (m *Manager) TransceiveWithDevice(deviceID string, req DeviceTransceiveRequest) (DeviceTransceiveResponse, error) {
	req.DeviceID = deviceID
	if req.TimeoutMS == 0 {
		req.TimeoutMS = int(DeviceTransceiveTimeout / time.Millisecond)
	}

	// Allow a little longer than the device was told to take, so a device
	// honouring its own deadline reports a real error rather than racing ours.
	timeout := time.Duration(req.TimeoutMS)*time.Millisecond + time.Second

	payload, err := m.request(deviceID, req.RequestID, WSTypeDeviceTransceiveRequest, req, timeout)
	if err != nil {
		return DeviceTransceiveResponse{}, err
	}

	var resp DeviceTransceiveResponse
	if err := decodePayload(payload, &resp); err != nil {
		return DeviceTransceiveResponse{}, fmt.Errorf("failed to parse transceive response: %w", err)
	}
	return resp, nil
}

// handleDeviceResponse delivers a device's response to whoever is waiting.
func (m *Manager) handleDeviceResponse(deviceID string, req protocol.WebSocketRequest) error {
	requestID, _ := req.Payload["requestID"].(string)
	if requestID == "" {
		requestID = req.ID
	}

	m.pendingMu.Lock()
	pending, ok := m.pending[requestID]
	if ok {
		delete(m.pending, requestID)
	}
	m.pendingMu.Unlock()

	if !ok {
		log.Printf("[device] Unmatched %s from %s: %s", req.Type, deviceID, requestID)
		return nil
	}

	// A device may only answer for its own requests.
	if pending.deviceID != deviceID {
		return fmt.Errorf("response for %s came from %s", pending.deviceID, deviceID)
	}

	pending.respCh <- pendingResult{payload: req.Payload}
	return nil
}

// failPendingRequests releases waiters blocked on a device that has gone away,
// rather than making each wait out the full timeout.
func (m *Manager) failPendingRequests(deviceID string) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()

	for requestID, pending := range m.pending {
		if pending.deviceID != deviceID {
			continue
		}
		pending.respCh <- pendingResult{
			err: &nfc.NFCError{
				Code:    nfc.ErrCodeTagNotConnected,
				Op:      "request",
				Message: "device disconnected before reporting the outcome",
			},
		}
		delete(m.pending, requestID)
	}
}

// writeTag writes an encoded NDEF message through the device holding the tag.
func (m *Manager) writeTag(deviceID, tagUID string, ndef []byte, opts nfc.WriteOptions) error {
	if !m.tagModificationAllowed() {
		return readOnlyModeError("WriteData", tagUID)
	}

	resp, err := m.WriteToDevice(deviceID, DeviceWriteRequest{
		RequestID: uuid.NewString(),
		TagUID:    tagUID,
		NDEFBytes: ndef,
		Lock:      opts.Lock,
		// Derived from the operation, not freshly generated: a key that differs
		// on every attempt can never match a replay, which is the only thing it
		// exists to catch.
		IdempotencyKey: operationKey(deviceID, tagUID, "write", ndef, opts.Lock),
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return writeResponseError("WriteData", tagUID, resp)
	}
	return nil
}

// lockTag makes the tag permanently read-only through the device holding it.
func (m *Manager) lockTag(deviceID, tagUID string) error {
	if !m.tagModificationAllowed() {
		return readOnlyModeError("MakeReadOnly", tagUID)
	}

	resp, err := m.WriteToDevice(deviceID, DeviceWriteRequest{
		RequestID:      uuid.NewString(),
		TagUID:         tagUID,
		Lock:           true,
		IdempotencyKey: operationKey(deviceID, tagUID, "lock", nil, true),
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return writeResponseError("MakeReadOnly", tagUID, resp)
	}
	return nil
}

// transceiveTag exchanges raw data with the tag through the device holding it.
// A raw exchange can carry a write, so read-only mode refuses it.
func (m *Manager) transceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	if !m.tagModificationAllowed() {
		return nil, readOnlyModeError("Transceive", tagUID)
	}

	resp, err := m.TransceiveWithDevice(deviceID, DeviceTransceiveRequest{
		RequestID: uuid.NewString(),
		TagUID:    tagUID,
		Data:      data,
		Raw:       raw,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		message := resp.Error
		if message == "" {
			message = "device reported the exchange failed"
		}
		return nil, &nfc.NFCError{
			Code:    protocol.InternalErrorCode(resp.ErrorCode, nfc.ErrCodeTransceiveFailed),
			Op:      "Transceive",
			TagUID:  tagUID,
			Message: message,
		}
	}
	return resp.Data, nil
}

func (m *Manager) deviceCanWrite(deviceID string) bool {
	return m.deviceDeclared(deviceID, func(c DeviceCapabilities) bool { return c.CanWrite })
}

func (m *Manager) deviceCanLock(deviceID string) bool {
	return m.deviceDeclared(deviceID, func(c DeviceCapabilities) bool { return c.CanLock })
}

func (m *Manager) deviceCanTransceive(deviceID string) bool {
	return m.deviceDeclared(deviceID, func(c DeviceCapabilities) bool { return c.CanTransceive })
}

// deviceDeclared reports whether a still-connected device declared a
// capability. Every capability routed through a session modifies a tag, so
// read-only mode withdraws all of them: a tag must not advertise an operation
// the manager would refuse.
func (m *Manager) deviceDeclared(deviceID string, want func(DeviceCapabilities) bool) bool {
	if !m.tagModificationAllowed() {
		return false
	}

	device, ok := m.GetDevice(deviceID)
	if !ok || !device.IsActive() {
		return false
	}

	if _, connected := m.session(deviceID); !connected {
		return false
	}
	return want(device.PhoneCapabilities())
}

// DeviceMaxHoldMs reports how long a device can keep a tag available for work,
// zero meaning open-ended. That is also the answer for a device that declared
// nothing and for one no longer connected, so treat it as advice about what is
// worth attempting rather than as permission.
func (m *Manager) DeviceMaxHoldMs(deviceID string) int {
	device, ok := m.GetDevice(deviceID)
	if !ok || !device.IsActive() {
		return 0
	}
	return device.PhoneCapabilities().MaxHoldMs
}

// tagModificationAllowed reports whether the agent's mode currently permits an
// operation that can change a tag.
func (m *Manager) tagModificationAllowed() bool {
	m.mu.RLock()
	allow := m.allowTagModification
	m.mu.RUnlock()

	return allow == nil || allow()
}

// operationKey identifies a logical tag operation, so the same one retried after
// a lost response carries the same key and a device can report the earlier
// outcome instead of applying it twice.
func operationKey(deviceID, tagUID, op string, payload []byte, lock bool) string {
	sum := sha256.New()
	for _, part := range []string{deviceID, tagUID, op} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	if lock {
		sum.Write([]byte{1})
	}
	sum.Write(payload)

	return hex.EncodeToString(sum.Sum(nil)[:16])
}

// readOnlyModeError refuses a tag-modifying operation on mode grounds, typed so
// the refusal survives as READ_ONLY.
func readOnlyModeError(op, tagUID string) error {
	return &nfc.NFCError{
		Code:    nfc.ErrCodeReadOnly,
		Op:      op,
		TagUID:  tagUID,
		Message: "agent is in read-only mode",
	}
}

// writeResponseError turns a device's refusal into a typed error, so the code
// it reported survives.
func writeResponseError(op, tagUID string, resp DeviceWriteResponse) error {
	message := resp.Error
	if message == "" {
		message = "device reported the operation failed"
	}

	fallback := nfc.ErrCodeWriteFailed
	if op == "MakeReadOnly" {
		fallback = nfc.ErrCodeReadOnly
	}

	return &nfc.NFCError{
		Code:    protocol.InternalErrorCode(resp.ErrorCode, fallback),
		Op:      op,
		TagUID:  tagUID,
		Message: message,
	}
}
