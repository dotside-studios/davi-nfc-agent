package ntag424

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

// A session is what authentication leaves behind: two keys, a transaction
// identifier the card assigned, and a counter that advances with every command.
//
// The counter and the identifier are what make a command usable once. Both go
// into every MAC, so a command captured from one session cannot be replayed
// into another, or into the same session twice.

// CommMode is how much protection a command carries. Which one a command needs
// is fixed by the card's file settings, not chosen freely: sending a command in
// the wrong mode is refused by the card.
type CommMode int

const (
	// CommPlain sends the command as it is. Still bound to the session, since
	// the card counts it.
	CommPlain CommMode = iota

	// CommMAC appends a MAC over the command, and checks one on the response.
	// The data travels in the clear.
	CommMAC

	// CommFull encrypts the data as well as MACing it.
	CommFull
)

// Session carries an authenticated transaction.
//
// It is not safe for concurrent use: every command advances the counter, and
// two goroutines sharing one session would send two commands with the same
// counter, which the card refuses.
type Session struct {
	ti       []byte
	encKey   []byte
	macKey   []byte
	encBlock cipher.Block
	macBlock cipher.Block

	// counter is the number of commands sent in this session. It goes into the
	// command's MAC, and the response's MAC carries it plus one.
	counter uint16
}

func newSession(ti, encKey, macKey []byte) (*Session, error) {
	if len(ti) != tiSize {
		return nil, fmt.Errorf("ntag424: transaction identifier is %d bytes, want %d", len(ti), tiSize)
	}
	encBlock, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	macBlock, err := aes.NewCipher(macKey)
	if err != nil {
		return nil, err
	}
	return &Session{
		ti:       append([]byte(nil), ti...),
		encKey:   encKey,
		macKey:   macKey,
		encBlock: encBlock,
		macBlock: macBlock,
	}, nil
}

// TI is the transaction identifier the card assigned. Carry it into an
// AuthenticateEV2NonFirst to stay in the same transaction.
func (s *Session) TI() []byte {
	return append([]byte(nil), s.ti...)
}

// Counter is how many commands this session has sent.
func (s *Session) Counter() uint16 {
	return s.counter
}

// Keys reports the session keys, for a caller driving a command this package
// does not build.
func (s *Session) Keys() (encKey, macKey []byte) {
	return append([]byte(nil), s.encKey...), append([]byte(nil), s.macKey...)
}

// Command builds the APDU for one command in this session.
//
// header is the part of the command the card reads in the clear even under
// CommFull, such as the file number; data is the part CommFull encrypts. The
// session's counter is not advanced here, because the command has not been
// answered yet: Response advances it once the card's answer is verified.
func (s *Session) Command(ins byte, header, data []byte, mode CommMode) ([]byte, error) {
	payload := append([]byte(nil), header...)

	switch mode {
	case CommPlain:
		payload = append(payload, data...)
		return wrapAPDU(ins, payload), nil

	case CommMAC:
		payload = append(payload, data...)

	case CommFull:
		if len(data) > 0 {
			encrypted, err := s.encrypt(data)
			if err != nil {
				return nil, err
			}
			payload = append(payload, encrypted...)
		}

	default:
		return nil, fmt.Errorf("ntag424: unknown communication mode %d", mode)
	}

	mac := s.commandMAC(ins, payload)
	return wrapAPDU(ins, append(payload, mac...)), nil
}

// Response verifies the card's answer to the command just sent and returns its
// data, decrypted under CommFull.
//
// The counter advances only on an answer that verifies, which keeps the two
// sides in step: a command the card never saw leaves the session unchanged.
func (s *Session) Response(response []byte, mode CommMode) ([]byte, error) {
	if len(response) < 2 {
		return nil, fmt.Errorf("ntag424: response is %d bytes, too short for a status word", len(response))
	}
	data, sw1, sw2 := response[:len(response)-2], response[len(response)-2], response[len(response)-1]
	if !statusOK(sw1, sw2) {
		return nil, fmt.Errorf("ntag424: card answered %02X%02X", sw1, sw2)
	}

	if mode == CommPlain {
		s.counter++
		return data, nil
	}

	if len(data) < MACSize {
		return nil, fmt.Errorf("ntag424: response carries %d bytes, too few for a MAC", len(data))
	}
	body, mac := data[:len(data)-MACSize], data[len(data)-MACSize:]

	if subtle.ConstantTimeCompare(s.responseMAC(sw2, body), mac) != 1 {
		return nil, ErrMACMismatch
	}

	if mode == CommFull && len(body) > 0 {
		plain, err := s.decrypt(body)
		if err != nil {
			return nil, err
		}
		body = plain
	}

	s.counter++
	return body, nil
}

// commandMAC is the MAC over what the card will read: the instruction, the
// counter, the transaction identifier, and the command's own bytes.
func (s *Session) commandMAC(ins byte, payload []byte) []byte {
	input := make([]byte, 0, 7+len(payload))
	input = append(input, ins)
	input = append(input, s.counterBytes()...)
	input = append(input, s.ti...)
	input = append(input, payload...)
	return truncateMAC(cmac(s.macBlock, input))
}

// responseMAC is the MAC the card computes over its answer. It carries the
// counter plus one, so a response cannot be replayed as the answer to the next
// command.
func (s *Session) responseMAC(status byte, body []byte) []byte {
	input := make([]byte, 0, 7+len(body))
	input = append(input, status)
	input = append(input, counterBytes(s.counter+1)...)
	input = append(input, s.ti...)
	input = append(input, body...)
	return truncateMAC(cmac(s.macBlock, input))
}

// encrypt enciphers command data under an IV built from this command's place in
// the session, so the same data sent twice never enciphers alike.
func (s *Session) encrypt(data []byte) ([]byte, error) {
	return s.encryptWithIV(s.iv(0xA5, 0x5A, s.counter), data), nil
}

// decrypt deciphers response data, whose IV is the command's with the two
// leading bytes swapped and the counter advanced, so a response cannot be fed
// back as a command.
func (s *Session) decrypt(data []byte) ([]byte, error) {
	return s.decryptWithIV(s.iv(0x5A, 0xA5, s.counter+1), data)
}

func (s *Session) encryptWithIV(iv, data []byte) []byte {
	padded := padISO9797(data)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(s.encBlock, iv).CryptBlocks(out, padded)
	return out
}

func (s *Session) decryptWithIV(iv, data []byte) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ntag424: encrypted data is %d bytes, want a multiple of %d", len(data), aes.BlockSize)
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(s.encBlock, iv).CryptBlocks(out, data)
	return unpadISO9797(out)
}

// iv builds a command's or response's initialisation vector by encrypting its
// place in the session under the session's own encryption key.
func (s *Session) iv(a, b byte, counter uint16) []byte {
	block := make([]byte, aes.BlockSize)
	block[0], block[1] = a, b
	copy(block[2:], s.ti)
	copy(block[6:], counterBytes(counter))

	iv := make([]byte, aes.BlockSize)
	s.encBlock.Encrypt(iv, block)
	return iv
}

func (s *Session) counterBytes() []byte {
	return counterBytes(s.counter)
}

// counterBytes writes the command counter least significant byte first, which
// is the order it travels in.
func counterBytes(counter uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, counter)
	return b
}

// padISO9797 appends the 0x80 marker and zeros out to a whole block, which is
// padding method 2. A message that already fills a block still gains one, so
// the padding is always removable.
func padISO9797(data []byte) []byte {
	out := append([]byte(nil), data...)
	out = append(out, 0x80)
	for len(out)%aes.BlockSize != 0 {
		out = append(out, 0x00)
	}
	return out
}

// unpadISO9797 removes that padding. Data whose last block is all zeros with no
// marker never came from padISO9797.
func unpadISO9797(data []byte) ([]byte, error) {
	for i := len(data) - 1; i >= 0 && i >= len(data)-aes.BlockSize; i-- {
		switch data[i] {
		case 0x00:
			continue
		case 0x80:
			return data[:i], nil
		default:
			return nil, fmt.Errorf("ntag424: decrypted response is not padded")
		}
	}
	return nil, fmt.Errorf("ntag424: decrypted response is not padded")
}
