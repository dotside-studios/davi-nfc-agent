package protocol

// Device bridge protocol versions. Version 0 is the original dialect
// (registerDevice/registerDeviceResponse) spoken by every device shipped before
// versioning existed; version 1 adds the hello handshake.
const (
	DeviceProtocolV0 = 0
	DeviceProtocolV1 = 1

	DeviceProtocolMax = DeviceProtocolV1
)

// SubprotocolDeviceV1 is offered by a v1 device in Sec-WebSocket-Protocol.
const SubprotocolDeviceV1 = "davi-nfc-device.v1"

// DeviceSubprotocols lists what the device endpoint accepts, in server
// preference order. A device offering none is still served, as version 0.
var DeviceSubprotocols = []string{SubprotocolDeviceV1}

// VersionFromSubprotocol maps a negotiated token to a version. Empty or
// unrecognized means version 0.
func VersionFromSubprotocol(sub string) int {
	switch sub {
	case SubprotocolDeviceV1:
		return DeviceProtocolV1
	default:
		return DeviceProtocolV0
	}
}

// NegotiateDeviceVersion resolves the version to speak with a device that
// declared `declared` in its hello payload. Sending hello at all implies v1, so
// a device declaring less is raised rather than rejected.
func NegotiateDeviceVersion(declared int) int {
	if declared < DeviceProtocolV1 {
		return DeviceProtocolV1
	}
	if declared > DeviceProtocolMax {
		return DeviceProtocolMax
	}
	return declared
}

// HelloRequest is the first frame from a v1 device. It folds registration into
// the version handshake so setup costs one round trip rather than two.
type HelloRequest struct {
	ProtocolVersion int `json:"protocolVersion"`
	DeviceRegistrationRequest
}

// HelloResponse reports the version both sides will speak, never higher than
// the device asked for.
type HelloResponse struct {
	ProtocolVersion int `json:"protocolVersion"`
	DeviceRegistrationResponse
}
