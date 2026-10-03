package remotenfc

import "github.com/dotside-studios/davi-nfc-agent/protocol"

// DeviceSequenceStep is one command of a deviceTransceiveSequenceRequest and
// the status words that decide whether the run goes on after it.
type DeviceSequenceStep struct {
	Data []byte `json:"data"` // Command bytes, base64 in transit

	// ExpectSW ends the run after this step unless the reply's status word is
	// one of these, each four hex characters. Omitted expects anything.
	ExpectSW []string `json:"expectSW,omitempty"`

	// StopOnSW ends the run after this step when the reply's status word is one
	// of these, each four hex characters.
	StopOnSW []string `json:"stopOnSW,omitempty"`
}

// DeviceTransceiveSequenceRequest asks a device to run several APDU-level
// exchanges with the tag it is holding, one after another with nothing else
// reaching the tag between them.
type DeviceTransceiveSequenceRequest struct {
	RequestID string               `json:"requestID"`
	DeviceID  string               `json:"deviceID"`
	TagUID    string               `json:"tagUID,omitempty"` // Report TAG_REMOVED if a different tag is present
	Steps     []DeviceSequenceStep `json:"steps"`

	// TimeoutMS bounds each exchange on the device.
	TimeoutMS int `json:"timeoutMs,omitempty"`
}

// DeviceTransceiveSequenceResponse carries the replies of the steps that ran.
type DeviceTransceiveSequenceResponse struct {
	RequestID string `json:"requestID"`
	Success   bool   `json:"success"`

	// Replies holds the whole reply of every step that ran, status word
	// included, in order.
	Replies [][]byte `json:"replies,omitempty"`

	// StoppedAt is the index of the step whose reply ended the run early, or -1
	// when every step ran. Always sent on success.
	StoppedAt int `json:"stoppedAt"`

	Error     string             `json:"error,omitempty"`
	ErrorCode protocol.ErrorCode `json:"errorCode,omitempty"`
}
