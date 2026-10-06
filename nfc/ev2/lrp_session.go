package ev2

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/lrp"
)

// The session LRP authentication leaves behind. It has the shape of [Session]:
// a transaction identifier, a command counter that goes into every MAC, and
// the three communication modes. What differs is the cipher: the MAC is CMAC_LRP
// and the encryption is LRICB, whose counter is a count of the blocks enciphered
// so far in the session rather than a value derived from the command counter.

// lrpEncCounterSize is the width of the LRICB counter, in bytes.
const lrpEncCounterSize = 4

// LRPSession carries an authenticated transaction on a card in LRP mode. Like
// [Session] it is not safe for concurrent use.
type LRPSession struct {
	ti     []byte
	macKey *lrp.Key
	encKey *lrp.Key

	counter uint16

	// encCounter is the number of blocks enciphered or deciphered so far,
	// which is the next LRICB counter. cmdBlocks is how many blocks the command
	// just built used, which the response that follows continues from, and
	// which are counted once the response verifies.
	encCounter uint32
	cmdBlocks  uint32
}

// NewLRPSession builds a session from the transaction identifier and the
// session's master key (see [DeriveLRPSessionKey]), with the command counter at
// counter and the enciphering counter at encCounter.
func NewLRPSession(ti, master []byte, counter uint16, encCounter uint32) (*LRPSession, error) {
	if len(ti) != TISize {
		return nil, fmt.Errorf("ev2: transaction identifier is %d bytes, want %d", len(ti), TISize)
	}
	macKey, err := lrp.New(master, lrp.UpdateMAC)
	if err != nil {
		return nil, err
	}
	encKey, err := lrp.New(master, lrp.UpdateENC)
	if err != nil {
		return nil, err
	}
	return &LRPSession{
		ti:         append([]byte(nil), ti...),
		macKey:     macKey,
		encKey:     encKey,
		counter:    counter,
		encCounter: encCounter,
	}, nil
}

// TI is the transaction identifier the card assigned.
func (s *LRPSession) TI() []byte { return append([]byte(nil), s.ti...) }

// Counter is how many commands this session has sent.
func (s *LRPSession) Counter() uint16 { return s.counter }

// Command builds the APDU for one command in this session. See
// [Session.Command].
func (s *LRPSession) Command(ins byte, header, data []byte, mode CommMode) ([]byte, error) {
	payload := append([]byte(nil), header...)
	s.cmdBlocks = 0

	switch mode {
	case CommPlain:
		payload = append(payload, data...)
		return WrapAPDU(ins, payload), nil

	case CommMAC:
		payload = append(payload, data...)

	case CommFull:
		if len(data) > 0 {
			padded := PadISO9797(data)
			encrypted, err := s.encKey.Encrypt(lrpCounterBytes(s.encCounter), padded)
			if err != nil {
				return nil, err
			}
			s.cmdBlocks = uint32(len(padded) / BlockSize)
			payload = append(payload, encrypted...)
		}

	default:
		return nil, fmt.Errorf("ev2: unknown communication mode %d", mode)
	}

	return WrapAPDU(ins, append(payload, s.commandMAC(ins, payload)...)), nil
}

// Response verifies the card's answer to the command just sent. See
// [Session.Response].
func (s *LRPSession) Response(response []byte, mode CommMode) ([]byte, error) {
	if len(response) < 2 {
		return nil, fmt.Errorf("ev2: response is %d bytes, too short for a status word", len(response))
	}
	data, sw1, sw2 := response[:len(response)-2], response[len(response)-2], response[len(response)-1]
	if !StatusOK(sw1, sw2) {
		return nil, fmt.Errorf("ev2: card answered %02X%02X", sw1, sw2)
	}

	if mode == CommPlain {
		s.counter++
		return data, nil
	}

	if len(data) < MACSize {
		return nil, fmt.Errorf("ev2: response carries %d bytes, too few for a MAC", len(data))
	}
	body, mac := data[:len(data)-MACSize], data[len(data)-MACSize:]
	if subtle.ConstantTimeCompare(s.responseMAC(sw2, body), mac) != 1 {
		return nil, ErrMACMismatch
	}

	used := s.cmdBlocks
	if mode == CommFull && len(body) > 0 {
		plain, err := s.decrypt(body, s.encCounter+s.cmdBlocks)
		if err != nil {
			return nil, err
		}
		used += uint32(len(body) / BlockSize)
		body = plain
	}

	s.counter++
	s.encCounter += used
	s.cmdBlocks = 0
	return body, nil
}

// VerifyCommand checks a command built by the other side of this session. See
// [Session.VerifyCommand].
func (s *LRPSession) VerifyCommand(apdu []byte, mode CommMode, headerLen int) (header, data []byte, err error) {
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
	s.cmdBlocks = 0

	if mode == CommFull && len(data) > 0 {
		plain, err := s.decrypt(data, s.encCounter)
		if err != nil {
			return nil, nil, err
		}
		s.cmdBlocks = uint32(len(data) / BlockSize)
		data = plain
	}
	return header, data, nil
}

// Answer builds the response to the command just verified, and counts it. See
// [Session.Answer].
func (s *LRPSession) Answer(status byte, data []byte, mode CommMode) ([]byte, error) {
	body := append([]byte(nil), data...)
	used := s.cmdBlocks

	switch mode {
	case CommPlain:
		s.counter++
		return append(body, 0x91, status), nil

	case CommFull:
		if len(body) > 0 {
			padded := PadISO9797(body)
			enc, err := s.encKey.Encrypt(lrpCounterBytes(s.encCounter+s.cmdBlocks), padded)
			if err != nil {
				return nil, err
			}
			used += uint32(len(padded) / BlockSize)
			body = enc
		}

	case CommMAC:

	default:
		return nil, fmt.Errorf("ev2: unknown communication mode %d", mode)
	}

	out := append(body, s.responseMAC(status, body)...)
	s.counter++
	s.encCounter += used
	s.cmdBlocks = 0
	return append(out, 0x91, status), nil
}

func (s *LRPSession) decrypt(data []byte, counter uint32) ([]byte, error) {
	if len(data)%BlockSize != 0 {
		return nil, fmt.Errorf("ev2: encrypted data is %d bytes, want a multiple of %d", len(data), BlockSize)
	}
	plain, err := s.encKey.Decrypt(lrpCounterBytes(counter), data)
	if err != nil {
		return nil, err
	}
	return UnpadISO9797(plain)
}

// commandMAC and responseMAC cover the same fields as under AES, with CMAC_LRP
// for the MAC.
func (s *LRPSession) commandMAC(ins byte, payload []byte) []byte {
	input := make([]byte, 0, 7+len(payload))
	input = append(input, ins)
	input = append(input, counterBytes(s.counter)...)
	input = append(input, s.ti...)
	input = append(input, payload...)
	return TruncateMAC(s.macKey.CMAC(input))
}

func (s *LRPSession) responseMAC(status byte, body []byte) []byte {
	input := make([]byte, 0, 7+len(body))
	input = append(input, status)
	input = append(input, counterBytes(s.counter+1)...)
	input = append(input, s.ti...)
	input = append(input, body...)
	return TruncateMAC(s.macKey.CMAC(input))
}

func lrpCounterBytes(counter uint32) []byte {
	b := make([]byte, lrpEncCounterSize)
	binary.BigEndian.PutUint32(b, counter)
	return b
}
