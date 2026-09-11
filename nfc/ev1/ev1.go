// Package ev1 implements the AES authentication a DESFire EV1 offers, and the
// message authentication that follows it.
//
// It is the generation before [ev2]: AuthenticateAES (0xAA) rather than
// AuthenticateEV2First, and a session with neither a transaction identifier nor
// a command counter. What holds the sequence together instead is the chaining
// vector. It starts at zero, every MAC continues the chain where the last one
// left off, and each MAC becomes the next starting point, so a command lifted
// out of its sequence no longer verifies.
//
// An EV2 or EV3 answers AuthenticateEV2First and should be driven through
// [ev2]; this is for a card that does not, which is every EV1.
//
// What is here covers plain and MACed communication. Enciphered communication,
// which this generation protects with a CRC-32 inside the ciphertext rather
// than a MAC beside it, is not implemented: see [Session.Command].
//
// The transport is the caller's. Each step returns the APDU to send and consumes
// the card's answer.
//
// Modelled on libfreefare's mifare_desfire.c and mifare_desfire_crypto.c, which
// have driven these cards for years, and pinned against them in the tests.
package ev1

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// Instruction bytes for the exchange.
const (
	insAuthenticateAES = 0xAA
	insAdditionalFrame = 0xAF
)

// randomSize is the length of each side's random number under AES.
const randomSize = 16

// MACSize is how much of a MAC travels: the leading eight bytes of the full
// CMAC. The generation after this one keeps the odd-indexed bytes instead, so
// the two truncations are not interchangeable.
const MACSize = 8

// Errors from an exchange.
var (
	// ErrAuthFailed reports that the card returned the wrong random number,
	// meaning the key is wrong or the exchange was tampered with.
	ErrAuthFailed = errors.New("ev1: authentication failed")

	// ErrAuthState reports the steps being taken out of order.
	ErrAuthState = errors.New("ev1: authentication step out of order")

	// ErrMACMismatch reports that the card's answer did not carry the MAC it
	// should have.
	ErrMACMismatch = errors.New("ev1: MAC does not match")

	// ErrEnciphered reports a file whose communication setting asks for
	// enciphered messaging, which this package does not implement.
	ErrEnciphered = errors.New("ev1: enciphered communication is not implemented")
)

// CommMode is how much protection a command carries.
type CommMode int

const (
	// CommPlain sends the command as it is. The card still MACs its answer,
	// and the chain still advances, so the exchange stays bound in sequence.
	CommPlain CommMode = iota

	// CommMAC appends a MAC to the command as well.
	CommMAC
)

// Authenticator drives one AuthenticateAES exchange, in two round trips:
// Command then Challenge, each answer handed to the next step, ending at Finish.
type Authenticator struct {
	keyNo  byte
	block  cipher.Block
	random io.Reader

	iv   []byte
	rndA []byte
	rndB []byte

	stage int
}

// NewAuthenticator prepares an exchange with the numbered key.
func NewAuthenticator(keyNo byte, key []byte) (*Authenticator, error) {
	block, err := ev2.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Authenticator{
		keyNo:  keyNo,
		block:  block,
		random: rand.Reader,
		iv:     make([]byte, aes.BlockSize),
	}, nil
}

// SetRandom replaces the source of this side's random number, so a test can
// reproduce a fixed exchange. The exchange is only sound while that number is
// unpredictable, so nothing outside a test should call this.
func (a *Authenticator) SetRandom(r io.Reader) {
	a.random = r
}

// Command is the first APDU to send.
func (a *Authenticator) Command() []byte {
	return ev2.WrapAPDU(insAuthenticateAES, []byte{a.keyNo})
}

// Challenge consumes the card's answer to Command and returns the second APDU.
//
// The card's random number arrives enciphered; ours goes back with it, rotated,
// so a replayed answer cannot pass. The chaining vector carries through both,
// which is what ties the two halves of the exchange together.
func (a *Authenticator) Challenge(response []byte) ([]byte, error) {
	if a.stage != 0 {
		return nil, ErrAuthState
	}

	data, err := frameData(response, randomSize)
	if err != nil {
		return nil, err
	}

	a.rndB = a.decrypt(data)

	a.rndA = make([]byte, randomSize)
	if _, err := io.ReadFull(a.random, a.rndA); err != nil {
		return nil, fmt.Errorf("ev1: reading a random number: %w", err)
	}

	token := make([]byte, 0, 2*randomSize)
	token = append(token, a.rndA...)
	token = append(token, rotateLeft(a.rndB)...)

	a.stage = 1
	return ev2.WrapAPDU(insAdditionalFrame, a.encrypt(token)), nil
}

// Finish consumes the card's answer to Challenge and returns the session. The
// card returns our own random number, rotated, which only a card holding the key
// could produce.
func (a *Authenticator) Finish(response []byte) (*Session, error) {
	if a.stage != 1 {
		return nil, ErrAuthState
	}

	data, err := frameData(response, randomSize)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(a.decrypt(data), rotateLeft(a.rndA)) {
		return nil, ErrAuthFailed
	}

	a.stage = 2
	return NewSession(SessionKey(a.rndA, a.rndB))
}

// SessionKey derives the key the session runs under: a quarter of each random
// number, taken from the front and the back of both.
func SessionKey(rndA, rndB []byte) []byte {
	key := make([]byte, 0, 16)
	key = append(key, rndA[0:4]...)
	key = append(key, rndB[0:4]...)
	key = append(key, rndA[12:16]...)
	key = append(key, rndB[12:16]...)
	return key
}

// encrypt enciphers whole blocks, continuing the chain and leaving it at the
// last block of ciphertext.
func (a *Authenticator) encrypt(data []byte) []byte {
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(a.block, a.iv).CryptBlocks(out, data)
	a.iv = append([]byte(nil), out[len(out)-aes.BlockSize:]...)
	return out
}

// decrypt deciphers whole blocks, continuing the chain and leaving it at the
// last block of ciphertext, which is what the card does with the same bytes.
func (a *Authenticator) decrypt(data []byte) []byte {
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(a.block, a.iv).CryptBlocks(out, data)
	a.iv = append([]byte(nil), data[len(data)-aes.BlockSize:]...)
	return out
}

// rotateLeft moves the first byte to the end, which is how each side proves it
// deciphered the other's number rather than replaying it.
func rotateLeft(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append(append([]byte(nil), b[1:]...), b[0])
}

// frameData takes the data out of a card's answer, checking the status word and
// the length.
func frameData(response []byte, size int) ([]byte, error) {
	if len(response) < 2 {
		return nil, fmt.Errorf("ev1: response is %d bytes, too short for a status word", len(response))
	}
	data, sw1, sw2 := response[:len(response)-2], response[len(response)-2], response[len(response)-1]

	ok := (sw1 == 0x91 || sw1 == 0x90) && (sw2 == 0x00 || sw2 == 0xAF)
	if !ok {
		return nil, fmt.Errorf("ev1: card answered %02X%02X", sw1, sw2)
	}
	if len(data) != size {
		return nil, fmt.Errorf("ev1: response carries %d bytes, want %d", len(data), size)
	}
	return data, nil
}

// Session is an authenticated exchange with an EV1. It is not safe for
// concurrent use: two goroutines sharing one would advance the same chain from
// two places, and neither command would verify.
type Session struct {
	key   []byte
	block cipher.Block

	// iv is the chaining vector every MAC continues from, and which every MAC
	// replaces. It starts at zero once authentication finishes.
	iv []byte
}

// NewSession builds a session over a key the authentication derived. Prefer
// authenticating; this is for resuming an exchange whose key you already hold,
// or reproducing a fixed one.
func NewSession(key []byte) (*Session, error) {
	block, err := ev2.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Session{
		key:   append([]byte(nil), key...),
		block: block,
		iv:    make([]byte, aes.BlockSize),
	}, nil
}

// Key reports the session key, for a caller driving a command this package does
// not build.
func (s *Session) Key() []byte {
	return append([]byte(nil), s.key...)
}

// Command builds the APDU for one command in this session.
//
// Under CommPlain the command travels exactly as it would unauthenticated: the
// MAC is computed and kept, because the card computes the same one and the
// chain has to stay in step, but it is not sent. Under CommMAC it is appended.
//
// An enciphered command is refused rather than sent unprotected.
func (s *Session) Command(ins byte, data []byte, mode CommMode) ([]byte, error) {
	native := append([]byte{ins}, data...)

	switch mode {
	case CommPlain:
		s.advance(native)
		return ev2.WrapAPDU(ins, data), nil

	case CommMAC:
		mac := s.advance(native)
		return ev2.WrapAPDU(ins, append(append([]byte(nil), data...), mac...)), nil

	default:
		return nil, ErrEnciphered
	}
}

// Response verifies the card's answer to the command just sent and returns its
// data.
//
// The card MACs its answer whichever mode the command travelled in, over the
// data it is returning followed by the status it is returning it with, so a
// status cannot be swapped for another.
func (s *Session) Response(response []byte, mode CommMode) ([]byte, error) {
	if len(response) < 2 {
		return nil, fmt.Errorf("ev1: response is %d bytes, too short for a status word", len(response))
	}
	data, sw1, sw2 := response[:len(response)-2], response[len(response)-2], response[len(response)-1]

	if (sw1 != 0x91 && sw1 != 0x90) || sw2 != 0x00 {
		return nil, fmt.Errorf("ev1: card answered %02X%02X", sw1, sw2)
	}
	if mode != CommPlain && mode != CommMAC {
		return nil, ErrEnciphered
	}

	if len(data) < MACSize {
		return nil, fmt.Errorf("ev1: response carries %d bytes, too few for a MAC", len(data))
	}
	body, mac := data[:len(data)-MACSize], data[len(data)-MACSize:]

	want := s.advance(append(append([]byte(nil), body...), sw2))
	if subtle.ConstantTimeCompare(want, mac) != 1 {
		return nil, ErrMACMismatch
	}
	return body, nil
}

// advance MACs a message, moves the chain on, and returns what travels: the
// leading bytes of the full CMAC.
func (s *Session) advance(msg []byte) []byte {
	full := ev2.CMACFrom(s.block, s.iv, msg)
	s.iv = full
	return append([]byte(nil), full[:MACSize]...)
}
