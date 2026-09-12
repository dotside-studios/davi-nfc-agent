package nfctest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev1"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// The card's half of the two authenticated channels a DESFire may offer, so the
// driver is tested against something that verifies what it sends rather than
// against a stub that accepts anything.

// emuChannel answers commands inside a session, whichever generation opened it.
type emuChannel interface {
	// verify checks a command the driver built and returns its header and data.
	verify(cmd []byte, comm byte, headerLen int) (header, data []byte, err error)

	// answer builds the response to the command just verified.
	answer(status byte, data []byte, comm byte) ([]byte, error)
}

// emuEV2Channel is the card side of an EV2 session, which ev2 implements for us.
type emuEV2Channel struct{ session *ev2.Session }

func (c emuEV2Channel) verify(cmd []byte, comm byte, headerLen int) ([]byte, []byte, error) {
	mode, err := emuCommMode(comm)
	if err != nil {
		return nil, nil, err
	}
	return c.session.VerifyCommand(cmd, mode, headerLen)
}

func (c emuEV2Channel) answer(status byte, data []byte, comm byte) ([]byte, error) {
	mode, err := emuCommMode(comm)
	if err != nil {
		return nil, err
	}
	return c.session.Answer(status, data, mode)
}

func emuCommMode(comm byte) (ev2.CommMode, error) {
	switch comm {
	case dfCommPlain:
		return ev2.CommPlain, nil
	case dfCommMAC:
		return ev2.CommMAC, nil
	case dfCommFull:
		return ev2.CommFull, nil
	default:
		return 0, fmt.Errorf("nfctest: unknown communication setting %#02x", comm)
	}
}

// emuEV1Channel is the card side of an EV1 session: no transaction identifier
// and no counter, a chain that every MAC continues and replaces, and the
// leading eight bytes of each MAC on the wire.
type emuEV1Channel struct {
	block cipher.Block
	iv    []byte
}

func newEMUEV1Channel(sessionKey []byte) (*emuEV1Channel, error) {
	block, err := ev2.NewCipher(sessionKey)
	if err != nil {
		return nil, err
	}
	return &emuEV1Channel{block: block, iv: make([]byte, aes.BlockSize)}, nil
}

// advance MACs a message, moves the chain on, and returns what would travel.
func (c *emuEV1Channel) advance(msg []byte) []byte {
	full := ev2.CMACFrom(c.block, c.iv, msg)
	c.iv = full
	return full[:ev1.MACSize]
}

func (c *emuEV1Channel) verify(cmd []byte, comm byte, headerLen int) ([]byte, []byte, error) {
	if comm == dfCommFull {
		return nil, nil, fmt.Errorf("nfctest: an EV1 session was asked for enciphered data")
	}

	ins, payload, err := unwrapAPDU(cmd)
	if err != nil {
		return nil, nil, err
	}

	// Under MACed communication the driver appends the MAC; under plain it
	// sends none, and only the chain moves.
	if comm == dfCommMAC {
		if len(payload) < ev1.MACSize {
			return nil, nil, fmt.Errorf("nfctest: command carries no MAC")
		}
		body, mac := payload[:len(payload)-ev1.MACSize], payload[len(payload)-ev1.MACSize:]
		if !bytes.Equal(c.advance(append([]byte{ins}, body...)), mac) {
			return nil, nil, ev1.ErrMACMismatch
		}
		payload = body
	} else {
		c.advance(append([]byte{ins}, payload...))
	}

	if len(payload) < headerLen {
		return nil, nil, fmt.Errorf("nfctest: command carries %d bytes, too few for a %d-byte header",
			len(payload), headerLen)
	}
	return payload[:headerLen], payload[headerLen:], nil
}

func (c *emuEV1Channel) answer(status byte, data []byte, comm byte) ([]byte, error) {
	if comm == dfCommFull {
		return nil, fmt.Errorf("nfctest: an EV1 session was asked for enciphered data")
	}

	// The MAC covers the data and the status it is returned with.
	mac := c.advance(append(append([]byte(nil), data...), status))
	out := append(append([]byte(nil), data...), mac...)
	return append(out, 0x91, status), nil
}

// unwrapAPDU takes apart the ISO-wrapped native command the driver sends.
func unwrapAPDU(apdu []byte) (ins byte, payload []byte, err error) {
	if len(apdu) < 5 || apdu[0] != 0x90 {
		return 0, nil, fmt.Errorf("nfctest: not an ISO-wrapped native command")
	}
	if len(apdu) == 5 {
		return apdu[1], nil, nil
	}
	lc := int(apdu[4])
	if len(apdu) != 6+lc {
		return 0, nil, fmt.Errorf("nfctest: command declares %d bytes and carries %d", lc, len(apdu)-6)
	}
	return apdu[1], apdu[5 : 5+lc], nil
}

// authenticateAES answers the older exchange with the card's random number,
// enciphered from a zero chain.
func (e *desfireEmulator) authenticateAES(body []byte) []byte {
	if len(body) < 1 {
		return dfResp(nil, 0x7E)
	}
	key, ok := e.keys[body[0]]
	if !ok {
		return dfResp(nil, dfStatusAuthError)
	}

	block, err := ev2.NewCipher(key)
	if err != nil {
		return dfResp(nil, dfStatusAuthError)
	}
	e.authKeyNo = body[0]
	e.authRndB = bytes.Repeat([]byte{0x5B}, 16)
	e.ev1IV = make([]byte, aes.BlockSize)

	out := make([]byte, len(e.authRndB))
	cipher.NewCBCEncrypter(block, e.ev1IV).CryptBlocks(out, e.authRndB)
	e.ev1IV = append([]byte(nil), out...)
	return dfResp(out, dfStatusAdditionalFrame)
}

// finishAuthAES consumes the driver's half of the older exchange and opens the
// session.
func (e *desfireEmulator) finishAuthAES(body []byte) []byte {
	key := e.keys[e.authKeyNo]
	block, err := ev2.NewCipher(key)
	if err != nil || len(body) != 32 {
		return dfResp(nil, dfStatusAuthError)
	}

	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, e.ev1IV).CryptBlocks(plain, body)
	e.ev1IV = append([]byte(nil), body[len(body)-aes.BlockSize:]...)

	rndA := append([]byte(nil), plain[:16]...)
	if !bytes.Equal(plain[16:], rotateLeft(e.authRndB)) {
		return dfResp(nil, dfStatusAuthError)
	}

	answer := make([]byte, 16)
	cipher.NewCBCEncrypter(block, e.ev1IV).CryptBlocks(answer, rotateLeft(rndA))

	channel, err := newEMUEV1Channel(ev1.SessionKey(rndA, e.authRndB))
	if err != nil {
		return dfResp(nil, dfStatusAuthError)
	}
	e.channel = channel
	return dfResp(answer, dfStatusOK)
}
