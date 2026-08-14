package server

import "github.com/dotside-studios/davi-nfc-agent/buildinfo"

// mDNS service discovery constants. The agent advertises a single service,
// under the device service type, on the one unified port.
var (
	MDNSDomain = "local."

	MDNSDeviceServiceType = "_nfc-device._tcp"
	MDNSDeviceServiceName = buildinfo.DisplayName + " Device"
)

// WebSocket message types for client-server communication
const (
	WSMessageTypeTagData       = "tagData"
	WSMessageTypeDeviceStatus  = "deviceStatus"
	WSMessageTypeWriteRequest  = "writeRequest"
	WSMessageTypeWriteResponse = "writeResponse"
	WSMessageTypeLockRequest   = "lockRequest"
	WSMessageTypeLockResponse  = "lockResponse"

	WSMessageTypeCapabilitiesRequest  = "capabilitiesRequest"
	WSMessageTypeCapabilitiesResponse = "capabilitiesResponse"

	WSMessageTypeError = "error"
)

// CORS configuration
const (
	CORSAllowOrigin  = "*"
	CORSAllowMethods = "GET, POST, OPTIONS"
	CORSAllowHeaders = "Content-Type, Authorization"
)
