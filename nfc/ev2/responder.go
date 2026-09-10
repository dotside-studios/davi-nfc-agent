package ev2

import (
	"crypto/subtle"
	"fmt"
)

// The card's half of a session. [Session.Command] and [Session.Response] drive
// one from the reader's side; these two verify and answer the same exchange
// from the card's, which is what a card emulator needs and what proves the two
// halves agree.
//
// The counter advances in Answer, as it advances in Response, so both sides
// count a command once and at the same point.

// DeriveSessionKeys derives the two session keys from both sides' random
// numbers, under the key the authentication proved. Neither side alone decides
// them.
//
// [Authenticator] does this itself; it is exported for the other side of the
// exchange, which has to arrive at the same keys.
func DeriveSessionKeys(key, rndA, rndB []byte) (encKey, macKey []byte, err error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	if len(rndA) != randomSize || len(rndB) != randomSize {
		return nil, nil, fmt.Errorf("ev2: random numbers are %d and %d bytes, want %d each",
			len(rndA), len(rndB), randomSize)
	}
	return CMAC(block, sessionVectorSSM(ssmSV1Prefix, rndA, rndB)),
		CMAC(block, sessionVectorSSM(ssmSV2Prefix, rndA, rndB)), nil
}

// VerifyCommand checks a command built by the other side of this session and
// returns its header and data, decrypted under CommFull.
//
// headerLen is how much of the payload the command carries in the clear, such
// as the file number and offset of a read. The counter does not advance here:
// the command has not been answered yet.
func (s *Session) VerifyCommand(apdu []byte, mode CommMode, headerLen int) (header, data []byte, err error) {
	ins, payload, err := unwrapAPDU(apdu)
	if err != nil {
		return nil, nil, err
	}

	if mode != CommPlain {
		if len(payload) < MACSize {
			return nil, nil, fmt.Errorf("ev2: command carries %d bytes, too few for a MAC", len(payload))
		}
		body, mac := payload[:len(payload)-MACSize], payload[len(payload)-MACSize:]
		if subtle.ConstantTimeCompare(s.commandMAC(ins, body), mac) != 1 {
			return nil, nil, ErrMACMismatch
		}
		payload = body
	}

	if len(payload) < headerLen {
		return nil, nil, fmt.Errorf("ev2: command carries %d bytes, too few for a %d-byte header",
			len(payload), headerLen)
	}
	header, data = payload[:headerLen], payload[headerLen:]

	if mode == CommFull && len(data) > 0 {
		plain, err := s.decryptWithIV(s.iv(0xA5, 0x5A, s.counter), data)
		if err != nil {
			return nil, nil, err
		}
		data = plain
	}
	return header, data, nil
}

// Answer builds the response to the command just verified, and counts it.
func (s *Session) Answer(status byte, data []byte, mode CommMode) ([]byte, error) {
	body := append([]byte(nil), data...)

	switch mode {
	case CommPlain:
		s.counter++
		return append(body, 0x91, status), nil

	case CommFull:
		if len(body) > 0 {
			body = s.encryptWithIV(s.iv(0x5A, 0xA5, s.counter+1), body)
		}

	case CommMAC:

	default:
		return nil, fmt.Errorf("ev2: unknown communication mode %d", mode)
	}

	out := append(body, s.responseMAC(status, body)...)
	s.counter++
	return append(out, 0x91, status), nil
}

// unwrapAPDU takes apart the ISO-wrapped native command WrapAPDU builds.
func unwrapAPDU(apdu []byte) (ins byte, payload []byte, err error) {
	// CLA, INS, P1, P2, Le at the least.
	if len(apdu) < 5 || apdu[0] != 0x90 {
		return 0, nil, fmt.Errorf("ev2: not an ISO-wrapped native command")
	}
	if len(apdu) == 5 {
		return apdu[1], nil, nil
	}

	lc := int(apdu[4])
	if len(apdu) != 6+lc {
		return 0, nil, fmt.Errorf("ev2: command declares %d bytes of data and carries %d", lc, len(apdu)-6)
	}
	return apdu[1], apdu[5 : 5+lc], nil
}
