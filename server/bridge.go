package server

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// ServerBridge facilitates communication between Device and Client servers.
// All channels are buffered to prevent blocking.
type ServerBridge struct {
	// TagData flows from Device -> Client when tags are scanned
	TagData chan nfc.NFCData

	// WriteRequest flows from Client -> Device for write operations
	WriteRequest chan WriteRequestMessage

	// LockRequest flows from Client -> Device for make-read-only operations
	LockRequest chan LockRequestMessage

	// CapabilitiesRequest flows from Client -> Device to query the present tag's
	// capabilities before a write/lock.
	CapabilitiesRequest chan CapabilitiesRequestMessage

	// Transceive flows from Client -> Device for raw exchanges with the tag.
	Transceive chan TransceiveRequestMessage

	// DeviceStatus flows from Device -> Client for device state updates
	DeviceStatus chan nfc.DeviceStatus

	// done signals when the bridge should stop
	done chan struct{}
}

// WriteRequestMessage wraps a write request with client identification.
type WriteRequestMessage struct {
	// RequestID correlates request with response
	RequestID string

	// ClientID identifies the requesting client
	ClientID string

	// TargetDevice names the remote device holding the tag to act on. Empty
	// leaves the routing to the agent, which prefers its own reader and then
	// the most recent scan.
	TargetDevice string

	// Request contains the actual write data
	Request WriteRequest

	// IdempotencyKey identifies the logical write, so a device that already
	// applied it can report the previous outcome instead of writing twice.
	IdempotencyKey string

	// ResponseCh receives the write result (buffered, size 1)
	ResponseCh chan WriteResponseMessage
}

// WriteResponseMessage wraps write operation results.
type WriteResponseMessage struct {
	// RequestID correlates with the original request
	RequestID string

	// Success indicates if the write succeeded
	Success bool

	// Error contains error message if Success is false
	Error string

	// ErrorCode classifies the failure so a client can tell a transient fault
	// from a refusal. Empty when Success is true.
	ErrorCode protocol.ErrorCode

	// Payload contains additional response data
	Payload any
}

// LockRequestMessage wraps a make-read-only request with client identification.
type LockRequestMessage struct {
	// RequestID correlates request with response
	RequestID string

	// ClientID identifies the requesting client
	ClientID string

	// TargetDevice names the remote device holding the tag to act on. Empty
	// leaves the routing to the agent, which prefers its own reader and then
	// the most recent scan.
	TargetDevice string

	// IdempotencyKey identifies the logical lock, so a device that already
	// applied it reports the previous outcome instead of locking twice.
	IdempotencyKey string

	// ResponseCh receives the lock result (buffered, size 1)
	ResponseCh chan LockResponseMessage
}

// LockResponseMessage wraps lock operation results.
type LockResponseMessage struct {
	// RequestID correlates with the original request
	RequestID string

	// Success indicates if the lock succeeded
	Success bool

	// Error contains error message if Success is false
	Error string

	// ErrorCode classifies the failure so a client can tell a transient fault
	// from a refusal. Empty when Success is true.
	ErrorCode protocol.ErrorCode

	// Payload contains additional response data
	Payload any
}

// TransceiveRequestMessage wraps a raw exchange with the present tag.
type TransceiveRequestMessage struct {
	// RequestID correlates request with response
	RequestID string

	// ClientID identifies the requesting client
	ClientID string

	// TargetDevice names the remote device holding the tag to act on. Empty
	// leaves the routing to the agent, which prefers its own reader and then
	// the most recent scan.
	TargetDevice string

	// Data is the command to send to the tag.
	Data []byte

	// Raw selects framing-level exchange over APDU-level, matching
	// protocol.DeviceTransceiveRequest.Raw.
	Raw bool

	// ResponseCh receives the exchange result (buffered, size 1)
	ResponseCh chan TransceiveResponseMessage
}

// TransceiveResponseMessage carries the tag's reply.
type TransceiveResponseMessage struct {
	// RequestID correlates with the original request
	RequestID string

	// Success indicates if the exchange completed. A tag answering with an
	// error status word is still a success here: the exchange happened, and
	// interpreting the status is the caller's job.
	Success bool

	// Data is the tag's response.
	Data []byte

	// Error contains error message if Success is false
	Error string

	// ErrorCode classifies the failure so a client can tell a transient fault
	// from a refusal. Empty when Success is true.
	ErrorCode protocol.ErrorCode
}

// CapabilitiesRequestMessage wraps a capabilities query with client identification.
type CapabilitiesRequestMessage struct {
	// RequestID correlates request with response
	RequestID string

	// ClientID identifies the requesting client
	ClientID string

	// TargetDevice names the remote device holding the tag to act on. Empty
	// leaves the routing to the agent, which prefers its own reader and then
	// the most recent scan.
	TargetDevice string

	// ResponseCh receives the capabilities result (buffered, size 1)
	ResponseCh chan CapabilitiesResponseMessage
}

// CapabilitiesResponseMessage wraps capabilities query results.
type CapabilitiesResponseMessage struct {
	// RequestID correlates with the original request
	RequestID string

	// Success indicates if the query succeeded
	Success bool

	// Error contains error message if Success is false
	Error string

	// ErrorCode classifies the failure so a client can tell a transient fault
	// from a refusal. Empty when Success is true.
	ErrorCode protocol.ErrorCode

	// Payload contains the tag capabilities (*nfc.TagCapabilities)
	Payload any
}

// NewServerBridge creates a new bridge with buffered channels.
func NewServerBridge() *ServerBridge {
	return &ServerBridge{
		TagData:             make(chan nfc.NFCData, 10),
		WriteRequest:        make(chan WriteRequestMessage, 10),
		LockRequest:         make(chan LockRequestMessage, 10),
		Transceive:          make(chan TransceiveRequestMessage, 10),
		CapabilitiesRequest: make(chan CapabilitiesRequestMessage, 10),
		DeviceStatus:        make(chan nfc.DeviceStatus, 10),
		done:                make(chan struct{}),
	}
}

// Close signals the bridge to stop.
//
// Only done is closed. Closing the data channels would race any producer
// already inside a send — the select on done cannot prevent that, and the
// losing goroutine panics on send to a closed channel. Consumers all exit on
// their own context instead, so the channels are simply abandoned.
func (b *ServerBridge) Close() {
	close(b.done)
}

// Done returns a channel that's closed when the bridge is shutting down.
func (b *ServerBridge) Done() <-chan struct{} {
	return b.done
}

// SendTagData sends tag data to the client server.
// Returns false if the bridge is closed or channel is full.
func (b *ServerBridge) SendTagData(data nfc.NFCData) bool {
	select {
	case <-b.done:
		return false
	case b.TagData <- data:
		return true
	default:
		// Channel full, drop the message
		return false
	}
}

// SendDeviceStatus sends device status to the client server.
// Returns false if the bridge is closed or channel is full.
func (b *ServerBridge) SendDeviceStatus(status nfc.DeviceStatus) bool {
	select {
	case <-b.done:
		return false
	case b.DeviceStatus <- status:
		return true
	default:
		// Channel full, drop the message
		return false
	}
}

// SendWriteRequest sends a write request to the device server and waits for response.
// Returns the response or an error if the bridge is closed.
func (b *ServerBridge) SendWriteRequest(msg WriteRequestMessage) (WriteResponseMessage, error) {
	// Ensure response channel is created
	if msg.ResponseCh == nil {
		msg.ResponseCh = make(chan WriteResponseMessage, 1)
	}

	select {
	case <-b.done:
		return WriteResponseMessage{}, ErrBridgeClosed
	case b.WriteRequest <- msg:
		// Wait for response
		select {
		case <-b.done:
			return WriteResponseMessage{}, ErrBridgeClosed
		case resp := <-msg.ResponseCh:
			return resp, nil
		}
	}
}

// SendLockRequest sends a make-read-only request to the device server and waits
// for the response. Returns the response or an error if the bridge is closed.
func (b *ServerBridge) SendLockRequest(msg LockRequestMessage) (LockResponseMessage, error) {
	// Ensure response channel is created
	if msg.ResponseCh == nil {
		msg.ResponseCh = make(chan LockResponseMessage, 1)
	}

	select {
	case <-b.done:
		return LockResponseMessage{}, ErrBridgeClosed
	case b.LockRequest <- msg:
		// Wait for response
		select {
		case <-b.done:
			return LockResponseMessage{}, ErrBridgeClosed
		case resp := <-msg.ResponseCh:
			return resp, nil
		}
	}
}

// SendCapabilitiesRequest sends a capabilities query to the device server and
// waits for the response. Returns the response or an error if the bridge is closed.
func (b *ServerBridge) SendCapabilitiesRequest(msg CapabilitiesRequestMessage) (CapabilitiesResponseMessage, error) {
	// Ensure response channel is created
	if msg.ResponseCh == nil {
		msg.ResponseCh = make(chan CapabilitiesResponseMessage, 1)
	}

	select {
	case <-b.done:
		return CapabilitiesResponseMessage{}, ErrBridgeClosed
	case b.CapabilitiesRequest <- msg:
		// Wait for response
		select {
		case <-b.done:
			return CapabilitiesResponseMessage{}, ErrBridgeClosed
		case resp := <-msg.ResponseCh:
			return resp, nil
		}
	}
}

// SendTransceiveRequest sends a raw exchange and waits for the tag's reply.
func (b *ServerBridge) SendTransceiveRequest(msg TransceiveRequestMessage) (TransceiveResponseMessage, error) {
	if msg.ResponseCh == nil {
		msg.ResponseCh = make(chan TransceiveResponseMessage, 1)
	}

	select {
	case <-b.done:
		return TransceiveResponseMessage{}, ErrBridgeClosed
	case b.Transceive <- msg:
		select {
		case <-b.done:
			return TransceiveResponseMessage{}, ErrBridgeClosed
		case resp := <-msg.ResponseCh:
			return resp, nil
		}
	}
}

// ErrBridgeClosed is returned when operations are attempted on a closed bridge.
var ErrBridgeClosed = &BridgeError{Message: "bridge is closed"}

// BridgeError represents errors from the bridge.
type BridgeError struct {
	Message string
}

func (e *BridgeError) Error() string {
	return e.Message
}
