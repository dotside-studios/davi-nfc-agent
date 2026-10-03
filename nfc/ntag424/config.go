package ntag424

import (
	"encoding/binary"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// Card configuration, key versions, the originality signature and the tamper
// status.

// Instruction bytes for the commands below.
const (
	insGetKeyVersion    = 0x64
	insSetConfiguration = 0x5C
	insReadSig          = 0x3C
	insGetTTStatus      = 0xF7
)

// SetConfiguration options.
const (
	ConfigPICC              = 0x00
	ConfigFailedAuthCounter = 0x0A
)

// SigSize is the length of the originality signature ReadSig returns.
const SigSize = 56

// piccConfigRandomID is the PICC configuration bit that makes the card answer
// with a random UID.
const piccConfigRandomID = 0x02

// GetKeyVersionPlain builds GetKeyVersion for a card not authenticated to.
func GetKeyVersionPlain(keyNo byte) []byte {
	return ev2.WrapAPDU(insGetKeyVersion, []byte{keyNo})
}

// GetKeyVersion builds the command that reads one key's version byte inside a
// session.
func GetKeyVersion(s Channel, keyNo byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: GetKeyVersion needs an authenticated session")
	}
	return s.Command(insGetKeyVersion, []byte{keyNo}, nil, CommMAC)
}

// ParseKeyVersion reads the card's answer to GetKeyVersion or
// GetKeyVersionPlain. With a nil session only the status is checked.
func ParseKeyVersion(s Channel, resp []byte) (byte, error) {
	data, err := CheckResponse(s, resp, CommMAC)
	if err != nil {
		return 0, err
	}
	if len(data) != 1 {
		return 0, fmt.Errorf("ntag424: key version is %d bytes, want 1", len(data))
	}
	return data[0], nil
}

// SetConfiguration builds the command that writes one of the card's
// configuration options. The typed helpers below cover the ones this package
// knows.
func SetConfiguration(s Channel, option byte, data []byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: SetConfiguration needs an authenticated session")
	}
	return s.Command(insSetConfiguration, []byte{option}, data, CommFull)
}

// SetRandomID builds the command that turns the random UID on or off. With it
// on the card reports a different 4-byte UID on every tap, and GetCardUID is the
// only way to learn the real one.
func SetRandomID(s Channel, enable bool) ([]byte, error) {
	var config byte
	if enable {
		config = piccConfigRandomID
	}
	return SetConfiguration(s, ConfigPICC, []byte{config})
}

// SetFailedAuthCounter builds the command that configures the card's
// failed-authentication counter: once the total failures reach limit the card
// delays authentication (91 AD), and each success lowers the total by decrement.
func SetFailedAuthCounter(s Channel, enable bool, limit, decrement uint16) ([]byte, error) {
	data := make([]byte, 5)
	if enable {
		data[0] = 0x01
	}
	binary.LittleEndian.PutUint16(data[1:], limit)
	binary.LittleEndian.PutUint16(data[3:], decrement)
	return SetConfiguration(s, ConfigFailedAuthCounter, data)
}

// ParseSetConfiguration verifies the card's answer to any SetConfiguration.
func ParseSetConfiguration(s Channel, resp []byte) error {
	_, err := CheckResponse(s, resp, CommFull)
	return err
}

// ReadSig builds the command that reads the card's originality signature.
func ReadSig(s Channel) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: ReadSig needs an authenticated session")
	}
	return s.Command(insReadSig, []byte{0x00}, nil, CommFull)
}

// ParseReadSig reads the card's answer to ReadSig: the 56-byte signature. It is
// not verified here.
func ParseReadSig(s Channel, resp []byte) ([]byte, error) {
	data, err := CheckResponse(s, resp, CommFull)
	if err != nil {
		return nil, err
	}
	if len(data) != SigSize {
		return nil, fmt.Errorf("ntag424: signature is %d bytes, want %d", len(data), SigSize)
	}
	return data, nil
}

// TTStatus is the state of a TagTamper wire.
type TTStatus struct {
	// Permanent is the status latched at the first opening, and Current the
	// wire's state now. Each is 'C' (closed), 'O' (open) or 'I' (invalid).
	Permanent, Current byte
}

// GetTTStatus builds the command that reads the tamper wire's status, on a tag
// that has one.
func GetTTStatus(s Channel) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: GetTTStatus needs an authenticated session")
	}
	return s.Command(insGetTTStatus, nil, nil, CommFull)
}

// ParseTTStatus reads the card's answer to GetTTStatus.
func ParseTTStatus(s Channel, resp []byte) (*TTStatus, error) {
	data, err := CheckResponse(s, resp, CommFull)
	if err != nil {
		return nil, err
	}
	if len(data) != 2 {
		return nil, fmt.Errorf("ntag424: tamper status is %d bytes, want 2", len(data))
	}
	return &TTStatus{Permanent: data[0], Current: data[1]}, nil
}
