package protocol

// WebSocket message type constants for consumer/client communication
const (
	WSTypeTagData       = "tagData"
	WSTypeDeviceStatus  = "deviceStatus"
	WSTypeWriteRequest  = "writeRequest"
	WSTypeWriteResponse = "writeResponse"
	WSTypeError         = "error"
)

// WebSocket message type constants for device communication
const (
	WSTypeHello                  = "hello"
	WSTypeHelloResponse          = "helloResponse"
	WSTypeGoodbye                = "goodbye"
	WSTypeRegisterDevice         = "registerDevice"
	WSTypeRegisterDeviceResponse = "registerDeviceResponse"
	WSTypeTagScanned             = "tagScanned"
	WSTypeTagRemoved             = "tagRemoved"
	WSTypeDeviceHeartbeat        = "deviceHeartbeat"
	WSTypeDeviceWriteRequest     = "deviceWriteRequest"
	WSTypeDeviceWriteResponse    = "deviceWriteResponse"
)

// WebSocketMessage is the generic message envelope for WebSocket communication.
type WebSocketMessage struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// WebSocketRequest is for incoming requests from WebSocket clients.
type WebSocketRequest struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

// WebSocketResponse is for responses to WebSocket requests.
type WebSocketResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Success bool   `json:"success"`
	Payload any    `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TagDataPayload is the payload structure broadcast when a tag is scanned.
type TagDataPayload struct {
	UID        string                 `json:"uid"`
	Type       string                 `json:"type"`
	Technology string                 `json:"technology"`
	ScannedAt  string                 `json:"scannedAt"` // RFC3339 format
	Text       string                 `json:"text"`      // Extracted text content
	Message    map[string]any `json:"message,omitempty"`
	Error      *string                `json:"err"`
}

// DeviceStatusPayload is the payload for device status updates.
type DeviceStatusPayload struct {
	Connected   bool   `json:"connected"`
	Message     string `json:"message"`
	CardPresent bool   `json:"cardPresent"`
}

// WriteRequestPayload is the payload for write requests.
type WriteRequestPayload struct {
	Records []WriteRecord `json:"records"`
}

// WriteRecord represents a single NDEF record in a write request.
type WriteRecord struct {
	Type     string `json:"type"`               // "text" or "uri"
	Content  string `json:"content"`            // Text or URI content
	Language string `json:"language,omitempty"` // Language code (default: "en")
}
