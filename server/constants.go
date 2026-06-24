package server

import "github.com/dotside-studios/davi-nfc-agent/buildinfo"

// mDNS service discovery constants for legacy single-server mode
var (
	MDNSServiceType = "_nfc-agent._tcp"
	MDNSServiceName = buildinfo.DisplayName
	MDNSDomain      = "local."
)

// mDNS service discovery constants for device server
var (
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
	WSMessageTypeError         = "error"
)

// CORS configuration
const (
	CORSAllowOrigin  = "*"
	CORSAllowMethods = "GET, POST, OPTIONS"
	CORSAllowHeaders = "Content-Type, Authorization"
)
