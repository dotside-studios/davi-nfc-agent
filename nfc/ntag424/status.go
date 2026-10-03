package ntag424

import (
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// Errors the card's status words map to, kept distinct because a driver
// answers each differently: a lost session is re-authenticated, a delay is
// waited out, a refusal is reported.
var (
	// ErrSessionLost reports that the card no longer holds the session: it was
	// reset, the command's integrity check failed, or the command length was
	// wrong (91 AE, 91 1E, 91 7E).
	ErrSessionLost = errors.New("ntag424: session lost")

	// ErrAuthDelay reports that the card refuses authentication for now, after
	// too many failures (91 AD). Trying again only extends the wait.
	ErrAuthDelay = errors.New("ntag424: authentication delayed")

	// ErrPermissionDenied reports that the file's access rights refuse the
	// operation (91 9D).
	ErrPermissionDenied = errors.New("ntag424: permission denied")

	// ErrLRP reports a tag in LRP mode that the caller has not allowed the
	// agent to authenticate to. See KeySet.AllowLRP.
	ErrLRP = errors.New("ntag424: tag is in LRP mode, which is not enabled")

	// ErrLRPKeyChange reports a ChangeKey on a session in LRP mode, which is
	// not implemented.
	ErrLRPKeyChange = errors.New("ntag424: changing a key in LRP mode is not supported")
)

// StatusError is a status word the card answered with other than success. It
// unwraps to ErrSessionLost, ErrAuthDelay or ErrPermissionDenied for the
// statuses that have one.
type StatusError struct {
	SW1, SW2 byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("ntag424: card answered %02X%02X", e.SW1, e.SW2)
}

func (e *StatusError) Unwrap() error {
	if e.SW1 != 0x91 {
		return nil
	}
	switch e.SW2 {
	case 0xAE, 0x1E, 0x7E:
		return ErrSessionLost
	case 0xAD:
		return ErrAuthDelay
	case 0x9D:
		return ErrPermissionDenied
	}
	return nil
}

// statusOf splits a response into its data and checks its status word.
func statusOf(resp []byte) ([]byte, error) {
	if len(resp) < 2 {
		return nil, fmt.Errorf("ntag424: response is %d bytes, too short for a status word", len(resp))
	}
	data, sw1, sw2 := resp[:len(resp)-2], resp[len(resp)-2], resp[len(resp)-1]
	if !ev2.StatusOK(sw1, sw2) {
		return nil, &StatusError{SW1: sw1, SW2: sw2}
	}
	return data, nil
}

// CheckResponse verifies a card's answer and returns its data: the status must
// be success and, with a session, the response MAC must verify and the data is
// decrypted under CommFull.
//
// With a nil session only the status is checked, which is right for the answer
// to a command sent outside one. Use it for ChangeKey and ChangeFileSettings,
// whose answers carry no data and whose MAC is the only proof the card applied
// the change.
func CheckResponse(s Channel, resp []byte, mode CommMode) ([]byte, error) {
	data, err := statusOf(resp)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return data, nil
	}
	return s.Response(resp, mode)
}

// CheckChangeKeyResponse verifies the answer to ChangeKey. Changing the key the
// session authenticated with ends the session, and the card answers with a bare
// status; any other key is answered with a MAC.
func CheckChangeKeyResponse(s Channel, resp []byte, keyNo, authKeyNo byte) error {
	if keyNo == authKeyNo {
		_, err := statusOf(resp)
		return err
	}
	_, err := CheckResponse(s, resp, CommFull)
	return err
}

// lrpAuthModeByte opens the card's first answer to an authentication in LRP
// mode, ahead of RndB.
const lrpAuthModeByte = 0x01

// IsLRPAuthResponse reports whether the card's answer to the first step of an
// authentication is an LRP one: 17 bytes, an authentication mode byte of 0x01
// then RndB, with status 91 AF. An AES-mode card answers with the 16 bytes of
// RndB alone.
func IsLRPAuthResponse(resp []byte) bool {
	if len(resp) != 1+ev2.BlockSize+2 {
		return false
	}
	return resp[len(resp)-2] == 0x91 && resp[len(resp)-1] == 0xAF && resp[0] == lrpAuthModeByte
}
