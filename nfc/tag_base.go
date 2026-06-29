package nfc

// CardTransport is the hardware boundary every PC/SC tag talks through: it sends
// an APDU and reports card presence. *pcscDevice satisfies it in production; an
// in-memory emulator satisfies it in tests (see package nfctest), letting the
// real tag I/O logic (page math, lock bytes, TLV) run against emulated silicon
// without hardware. Wrap one in a driver with NewEmulatedTag.
type CardTransport interface {
	Transceive(cmd []byte) ([]byte, error)
	IsCardPresent() bool
}

// pcscBaseTag provides common functionality for PC/SC tag implementations
type pcscBaseTag struct {
	device       CardTransport
	uid          string
	detectedType DetectedTagType
}

func (t *pcscBaseTag) UID() string {
	return t.uid
}

// Connect and Disconnect are no-ops: PC/SC tags are returned ready-to-use from
// GetTags() and the framework never calls these. They exist only to satisfy the
// TagConnection part of the Tag interface.
func (t *pcscBaseTag) Connect() error {
	return nil
}

func (t *pcscBaseTag) Disconnect() error {
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
