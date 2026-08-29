package nfc

import "fmt"

// CardTransport is the hardware boundary every PC/SC tag talks through: it sends
// an APDU and reports card presence. The PC/SC reader device satisfies it in
// production (see package nfc/pcsc); an
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

// ndefAreaLocked reports whether page 4 — the first NDEF user page of a
// page-oriented NTAG/Ultralight tag — is locked, by reading the static lock
// bytes. A tag locked with MakeReadOnly sets these, so a write to it would be
// NAK'd page by page with an opaque status word and burn the write path's whole
// retry budget on a permanent condition. Reading the lock bytes lets the driver
// recognise the locked tag up front and refuse the write with a typed read-only
// error the caller can act on. A read error (e.g. the card was removed) is
// returned to the caller rather than masked as "unlocked".
func (t *pcscBaseTag) ndefAreaLocked() (bool, error) {
	resp, err := t.transceive(ReadBinaryAPDU(2, 4))
	if err != nil {
		return false, err
	}
	if len(resp) < 3 {
		return false, fmt.Errorf("short read of static lock bytes: got %d of 4", len(resp))
	}
	// Static lock byte 0 is byte 2 of page 2; its bit 4 locks page 4, the first
	// NDEF page (NTAG21x / Ultralight datasheets, mirrored by the test emulator's
	// lock model). A MakeReadOnly'd tag sets this byte to 0xFF.
	return resp[2]&(1<<4) != 0, nil
}
