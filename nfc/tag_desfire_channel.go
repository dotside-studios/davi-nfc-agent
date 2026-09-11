package nfc

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev1"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// The two authenticated channels a DESFire may offer. An EV2 or EV3 answers
// AuthenticateEV2First and runs the session in nfc/ev2; an EV1 answers
// AuthenticateAES and runs the older one in nfc/ev1, which has no transaction
// identifier and no counter, and binds its commands with a chained MAC instead.
//
// The driver above this cares about neither, only that a command it hands over
// comes back verified.

// desfireChannel is an authenticated exchange with a DESFire, whichever
// generation's secure messaging carries it.
type desfireChannel interface {
	// command builds the APDU for one command. header is the part the card
	// reads in the clear even when the rest is enciphered; comm is the file's
	// communication setting.
	command(ins byte, header, data []byte, comm byte) ([]byte, error)

	// response verifies the card's answer to that command and returns its data.
	response(raw []byte, comm byte) ([]byte, error)
}

// commMode maps a file's communication setting onto the EV2 session's.
func commMode(setting byte) (ev2.CommMode, error) {
	switch setting {
	case dfCommPlain:
		return ev2.CommPlain, nil
	case dfCommMAC:
		return ev2.CommMAC, nil
	case dfCommFull:
		return ev2.CommFull, nil
	default:
		return 0, fmt.Errorf("unknown communication setting %#02x", setting)
	}
}

// ev2Channel carries the EV2 session.
type ev2Channel struct{ session *ev2.Session }

func (c ev2Channel) command(ins byte, header, data []byte, comm byte) ([]byte, error) {
	mode, err := commMode(comm)
	if err != nil {
		return nil, err
	}
	return c.session.Command(ins, header, data, mode)
}

func (c ev2Channel) response(raw []byte, comm byte) ([]byte, error) {
	mode, err := commMode(comm)
	if err != nil {
		return nil, err
	}
	return c.session.Response(raw, mode)
}

// ev1Channel carries the EV1 session. That generation makes no distinction
// between the cleartext head of a command and the rest, because the modes it
// implements here send the whole command in the clear either way.
type ev1Channel struct{ session *ev1.Session }

func (c ev1Channel) command(ins byte, header, data []byte, comm byte) ([]byte, error) {
	mode, err := ev1Mode(comm)
	if err != nil {
		return nil, err
	}
	return c.session.Command(ins, append(append([]byte(nil), header...), data...), mode)
}

func (c ev1Channel) response(raw []byte, comm byte) ([]byte, error) {
	mode, err := ev1Mode(comm)
	if err != nil {
		return nil, err
	}
	return c.session.Response(raw, mode)
}

// ev1Mode maps a file's communication setting onto what the EV1 session
// implements. Enciphered files are refused rather than driven wrongly: that
// generation protects them with a checksum inside the ciphertext, which is a
// scheme nfc/ev1 does not carry.
func ev1Mode(comm byte) (ev1.CommMode, error) {
	switch comm {
	case dfCommPlain:
		return ev1.CommPlain, nil
	case dfCommMAC:
		return ev1.CommMAC, nil
	case dfCommFull:
		return 0, fmt.Errorf("%w, which this file asks for", ev1.ErrEnciphered)
	default:
		return 0, fmt.Errorf("unknown communication setting %#02x", comm)
	}
}

// authenticateEV2 runs the newer exchange, which an EV2 or EV3 answers.
func (t *pcscDESFireTag) authenticateEV2(keyNo byte, key []byte) (desfireChannel, error) {
	auth, err := ev2.NewAuthenticator(ev2.AuthFirst, keyNo, key, nil)
	if err != nil {
		return nil, err
	}

	first, err := t.transmitRaw(auth.Command())
	if err != nil {
		return nil, err
	}
	second, err := auth.Challenge(first)
	if err != nil {
		return nil, err
	}
	answer, err := t.transmitRaw(second)
	if err != nil {
		return nil, err
	}
	session, err := auth.Finish(answer)
	if err != nil {
		return nil, err
	}
	return ev2Channel{session: session}, nil
}

// authenticateEV1 runs the older exchange, which every DESFire answers.
func (t *pcscDESFireTag) authenticateEV1(keyNo byte, key []byte) (desfireChannel, error) {
	auth, err := ev1.NewAuthenticator(keyNo, key)
	if err != nil {
		return nil, err
	}

	first, err := t.transmitRaw(auth.Command())
	if err != nil {
		return nil, err
	}
	second, err := auth.Challenge(first)
	if err != nil {
		return nil, err
	}
	answer, err := t.transmitRaw(second)
	if err != nil {
		return nil, err
	}
	session, err := auth.Finish(answer)
	if err != nil {
		return nil, err
	}
	return ev1Channel{session: session}, nil
}

// authenticators are the exchanges to try for a card, in order. A generation
// that was named gets the one it speaks and no other; a DESFire whose
// generation is unknown tries the newer exchange first and falls back, since a
// card that does not implement it refuses it without changing anything.
func (t *pcscDESFireTag) authenticators() []func(byte, []byte) (desfireChannel, error) {
	switch t.detectedType {
	case DetectedDESFireEV1:
		return []func(byte, []byte) (desfireChannel, error){t.authenticateEV1}
	case DetectedDESFireEV2, DetectedDESFireEV3:
		return []func(byte, []byte) (desfireChannel, error){t.authenticateEV2}
	default:
		return []func(byte, []byte) (desfireChannel, error){t.authenticateEV2, t.authenticateEV1}
	}
}
