package server

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// ServerBridge facilitates communication between Device and Client servers.
// All channels are buffered to prevent blocking.
type ServerBridge struct {
	// TagData flows from Device -> Client when tags are scanned
	TagData chan nfc.NFCData

	// WriteRequest flows from Client -> Device for write operations
	WriteRequest chan WriteRequestMessage

	// LockRequest flows from Client -> Device for make-read-only operations
	LockRequest chan LockRequestMessage

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

	// Request contains the actual write data
	Request WriteRequest

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

	// Payload contains additional response data
	Payload any
}

// LockRequestMessage wraps a make-read-only request with client identification.
type LockRequestMessage struct {
	// RequestID correlates request with response
	RequestID string

	// ClientID identifies the requesting client
	ClientID string

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

	// Payload contains additional response data
	Payload any
}

// NewServerBridge creates a new bridge with buffered channels.
func NewServerBridge() *ServerBridge {
	return &ServerBridge{
		TagData:      make(chan nfc.NFCData, 10),
		WriteRequest: make(chan WriteRequestMessage, 10),
		LockRequest:  make(chan LockRequestMessage, 10),
		DeviceStatus: make(chan nfc.DeviceStatus, 10),
		done:         make(chan struct{}),
	}
}

// Close signals the bridge to stop and closes all channels.
func (b *ServerBridge) Close() {
	close(b.done)
	close(b.TagData)
	close(b.WriteRequest)
	close(b.LockRequest)
	close(b.DeviceStatus)
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

// ErrBridgeClosed is returned when operations are attempted on a closed bridge.
var ErrBridgeClosed = &BridgeError{Message: "bridge is closed"}

// BridgeError represents errors from the bridge.
type BridgeError struct {
	Message string
}

func (e *BridgeError) Error() string {
	return e.Message
}
