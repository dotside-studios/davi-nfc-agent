package server

import (
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// mDNS service discovery constants. The agent advertises a single service,
// under the device service type, on the one unified port.
var (
	MDNSDomain = "local."

	MDNSDeviceServiceType = "_nfc-device._tcp"
	MDNSDeviceServiceName = buildinfo.DisplayName + " Device"
)

// WebSocket message types for client-server communication. The values live in
// protocol beside the envelope that carries them; these names are what the
// server has always called them.
const (
	WSMessageTypeTagData       = protocol.WSTypeTagData
	WSMessageTypeDeviceStatus  = protocol.WSTypeDeviceStatus
	WSMessageTypeWriteRequest  = protocol.WSTypeWriteRequest
	WSMessageTypeWriteResponse = protocol.WSTypeWriteResponse
	WSMessageTypeLockRequest   = protocol.WSTypeLockRequest
	WSMessageTypeLockResponse  = protocol.WSTypeLockResponse

	WSMessageTypeCapabilitiesRequest  = protocol.WSTypeCapabilitiesRequest
	WSMessageTypeCapabilitiesResponse = protocol.WSTypeCapabilitiesResponse

	WSMessageTypeTransceiveRequest  = protocol.WSTypeTransceiveRequest
	WSMessageTypeTransceiveResponse = protocol.WSTypeTransceiveResponse

	WSMessageTypeError = protocol.WSTypeError
)

// CORS configuration
const (
	CORSAllowOrigin  = "*"
	CORSAllowMethods = "GET, POST, OPTIONS"
	CORSAllowHeaders = "Content-Type, Authorization"
)
