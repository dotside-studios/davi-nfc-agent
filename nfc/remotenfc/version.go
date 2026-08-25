package remotenfc

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

// GoodbyeRequest is sent by a v1 device that is leaving deliberately, so the
// agent can tell an intentional departure from a dropped connection.
type GoodbyeRequest struct {
	DeviceID string `json:"deviceID,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// DisconnectReason explains why a device's session ended.
type DisconnectReason string

const (
	// DisconnectGoodbye means the device announced its departure.
	DisconnectGoodbye DisconnectReason = "goodbye"
	// DisconnectClosed means the connection closed cleanly without a goodbye.
	DisconnectClosed DisconnectReason = "closed"
	// DisconnectDropped means the connection went away without a close
	// handshake: the device crashed, lost its radio, or was killed.
	DisconnectDropped DisconnectReason = "dropped"
)

// Expected reports whether the device left on purpose. A dropped device may
// well come back; one that said goodbye should not be waited on.
func (r DisconnectReason) Expected() bool {
	return r == DisconnectGoodbye || r == DisconnectClosed
}
