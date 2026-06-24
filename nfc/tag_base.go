package nfc

// cardTransport is the hardware boundary every PC/SC tag talks through: it sends
// an APDU and reports card presence. *pcscDevice satisfies it in production; an
// in-memory emulator can satisfy it in tests, letting the real tag I/O logic
// (page math, lock bytes, TLV) run against emulated silicon without hardware.
type cardTransport interface {
	Transceive(cmd []byte) ([]byte, error)
	IsCardPresent() bool
}

// pcscBaseTag provides common functionality for PC/SC tag implementations
type pcscBaseTag struct {
	device       cardTransport
	uid          string
	detectedType DetectedTagType
	connected    bool
}

func (t *pcscBaseTag) UID() string {
	return t.uid
}

func (t *pcscBaseTag) Connect() error {
	t.connected = true
	return nil
}

func (t *pcscBaseTag) Disconnect() error {
	t.connected = false
	return nil
}

// transceive sends an APDU and returns the response data.
// Card removal detection is handled at the device layer via Transceive().
func (t *pcscBaseTag) transceive(cmd []byte) ([]byte, error) {
	resp, err := t.device.Transceive(cmd)
	if err != nil {
		return nil, err // Device layer already wraps card removal errors
	}

	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return nil, err
	}

	if !parsed.IsSuccess() && !parsed.HasMoreData() {
		return nil, parsed.Error()
	}

	return parsed.Data, nil
}

// transmitRaw sends an APDU and returns the raw response (with SW bytes).
// Card removal detection is handled at the device layer via Transceive().
func (t *pcscBaseTag) transmitRaw(cmd []byte) ([]byte, error) {
	return t.device.Transceive(cmd)
}
