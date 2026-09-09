package ntag424

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Authentication, which a command that changes a tag needs and SDM verification
// does not. It proves both sides hold the same AES key without either sending
// it, and leaves a session: two keys, a transaction identifier and a command
// counter.
//
// The transport is the caller's. Each step returns the APDU to send and consumes
// the card's answer.

// AuthMode selects which authentication a session starts with.
type AuthMode int

const (
	// AuthFirst is Cmd.AuthenticateEV2First, which begins a new transaction and
	// is answered with a transaction identifier.
	AuthFirst AuthMode = iota

	// AuthNonFirst is Cmd.AuthenticateEV2NonFirst, which authenticates again
	// inside a transaction that is already running. The card does not send a
	// transaction identifier back, so the one from the first authentication
	// carries over.
	AuthNonFirst
)

// Instruction bytes for the authentication exchange.
const (
	insAuthFirst       = 0x71
	insAuthNonFirst    = 0x77
	insAdditionalFrame = 0xAF
)

// Sizes the exchange works in.
const (
	randomSize = 16 // RndA and RndB
	tiSize     = 4  // transaction identifier
)

// Errors from an authentication exchange.
var (
	// ErrAuthFailed reports that the card returned the wrong random number,
	// meaning the key is wrong or the exchange was tampered with.
	ErrAuthFailed = errors.New("ntag424: authentication failed")

	// ErrAuthState reports the steps being taken out of order.
	ErrAuthState = errors.New("ntag424: authentication step out of order")
)

// Authenticator drives one authentication exchange, in two round trips: Command
// then Challenge, each answer handed to the next step, ending at Finish.
type Authenticator struct {
	mode   AuthMode
	keyNo  byte
	block  cipher.Block
	random io.Reader

	// ti carries over from an earlier authentication, for AuthNonFirst.
	ti []byte

	rndA  []byte
	rndB  []byte
	stage int
}

// NewAuthenticator prepares an exchange with the numbered key. For AuthNonFirst
// pass the transaction identifier from the first authentication; for AuthFirst
// pass nil, since the card assigns one.
func NewAuthenticator(mode AuthMode, keyNo byte, key, ti []byte) (*Authenticator, error) {
	block, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	if mode == AuthNonFirst && len(ti) != tiSize {
		return nil, fmt.Errorf("ntag424: AuthNonFirst needs the transaction identifier from the first authentication")
	}
	return &Authenticator{
		mode:   mode,
		keyNo:  keyNo,
		block:  block,
		random: rand.Reader,
		ti:     append([]byte(nil), ti...),
	}, nil
}

// SetRandom replaces the source of this side's random number, so a test can
// reproduce a published exchange. The exchange is only sound while that number
// is unpredictable, so nothing outside a test should call this.
func (a *Authenticator) SetRandom(r io.Reader) {
	a.random = r
}

// Command is the first APDU to send.
func (a *Authenticator) Command() []byte {
	if a.mode == AuthNonFirst {
		// NonFirst sends the key number alone; First adds a capability length.
		return wrapAPDU(insAuthNonFirst, []byte{a.keyNo})
	}
	return wrapAPDU(insAuthFirst, []byte{a.keyNo, 0x00})
}

// Challenge consumes the card's answer to Command and returns the second APDU.
// It decrypts the card's random number, generates ours, and sends both back with
// the card's rotated, so a replayed answer cannot pass.
func (a *Authenticator) Challenge(response []byte) ([]byte, error) {
	if a.stage != 0 {
		return nil, ErrAuthState
	}

	data, err := frameData(response, randomSize)
	if err != nil {
		return nil, err
	}
	a.rndB = make([]byte, randomSize)
	decryptCBC(a.block, a.rndB, data)

	a.rndA = make([]byte, randomSize)
	if _, err := io.ReadFull(a.random, a.rndA); err != nil {
		return nil, fmt.Errorf("ntag424: reading a random number: %w", err)
	}

	payload := make([]byte, 0, 2*randomSize)
	payload = append(payload, a.rndA...)
	payload = append(payload, rotateLeft(a.rndB)...)

	encrypted := make([]byte, len(payload))
	encryptCBC(a.block, encrypted, payload)

	a.stage = 1
	return wrapAPDU(insAdditionalFrame, encrypted), nil
}

// Finish consumes the card's answer to Challenge and returns the session. The
// card returns our own random number, rotated, which only a card holding the key
// could produce.
func (a *Authenticator) Finish(response []byte) (*Session, error) {
	if a.stage != 1 {
		return nil, ErrAuthState
	}

	// AuthenticateEV2First answers with the transaction identifier, our number
	// and both sides' capabilities; NonFirst answers with the number alone.
	size := randomSize
	if a.mode == AuthFirst {
		size = tiSize + randomSize + 12 // TI, RndA', PDcap2, PCDcap2
	}
	data, err := frameData(response, size)
	if err != nil {
		return nil, err
	}

	plain := make([]byte, len(data))
	decryptCBC(a.block, plain, data)

	ti := a.ti
	rotated := plain
	if a.mode == AuthFirst {
		ti = append([]byte(nil), plain[:tiSize]...)
		rotated = plain[tiSize : tiSize+randomSize]
	}

	if !bytes.Equal(rotateRight(rotated), a.rndA) {
		return nil, ErrAuthFailed
	}

	a.stage = 2
	encKey := cmac(a.block, sessionVectorSSM(ssmSV1Prefix, a.rndA, a.rndB))
	macKey := cmac(a.block, sessionVectorSSM(ssmSV2Prefix, a.rndA, a.rndB))
	return newSession(ti, encKey, macKey)
}

// Session vector prefixes for secure messaging. Not interchangeable with the SDM
// ones above, which derive keys for a tap rather than a transaction.
var (
	ssmSV1Prefix = []byte{0xA5, 0x5A, 0x00, 0x01, 0x00, 0x80}
	ssmSV2Prefix = []byte{0x5A, 0xA5, 0x00, 0x01, 0x00, 0x80}
)

// sessionVectorSSM builds the 32-byte input the session keys are derived from.
// Both random numbers are folded in, so neither side alone decides the keys.
func sessionVectorSSM(prefix, rndA, rndB []byte) []byte {
	sv := make([]byte, 0, 32)
	sv = append(sv, prefix...)
	sv = append(sv, rndA[0:2]...)
	for i := 0; i < 6; i++ {
		sv = append(sv, rndA[2+i]^rndB[i])
	}
	sv = append(sv, rndB[6:16]...)
	sv = append(sv, rndA[8:16]...)
	return sv
}

// rotateLeft returns b with its first byte moved to the end. Rotating the other
// side's number is what proves it was decrypted rather than replayed.
func rotateLeft(b []byte) []byte {
	return append(append([]byte(nil), b[1:]...), b[0])
}

// rotateRight undoes rotateLeft.
func rotateRight(b []byte) []byte {
	return append([]byte{b[len(b)-1]}, b[:len(b)-1]...)
}

// frameData returns the data of a card response, checking its status word and
// its length. A card mid-exchange answers 91 AF, and a completed one 91 00.
func frameData(response []byte, size int) ([]byte, error) {
	if len(response) < 2 {
		return nil, fmt.Errorf("ntag424: response is %d bytes, too short for a status word", len(response))
	}
	data, sw1, sw2 := response[:len(response)-2], response[len(response)-2], response[len(response)-1]

	if !statusOK(sw1, sw2) && !statusMoreFrames(sw1, sw2) {
		return nil, fmt.Errorf("ntag424: card answered %02X%02X", sw1, sw2)
	}
	if len(data) != size {
		return nil, fmt.Errorf("ntag424: response carries %d bytes, want %d", len(data), size)
	}
	return data, nil
}

// statusOK reports a status word meaning the command succeeded. 91 00 is the
// card's own encoding; 90 00 is a reader that unwraps it.
func statusOK(sw1, sw2 byte) bool {
	return (sw1 == 0x91 || sw1 == 0x90) && sw2 == 0x00
}

// statusMoreFrames reports the card asking for the next frame of an exchange,
// which is how it answers the first half of an authentication.
func statusMoreFrames(sw1, sw2 byte) bool {
	return sw1 == 0x91 && sw2 == 0xAF
}

// wrapAPDU builds the ISO-wrapped native command the card expects: CLA 90, the
// instruction, and the data with a trailing Le.
func wrapAPDU(ins byte, data []byte) []byte {
	cmd := make([]byte, 0, 6+len(data))
	cmd = append(cmd, 0x90, ins, 0x00, 0x00)
	if len(data) > 0 {
		cmd = append(cmd, byte(len(data)))
		cmd = append(cmd, data...)
	}
	return append(cmd, 0x00)
}

// encryptCBC and decryptCBC run AES-CBC with a zero IV, which is what the
// authentication exchange uses: each message is self-contained, and the session
// that follows derives its own IV per command.
func encryptCBC(block cipher.Block, dst, src []byte) {
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(dst, src)
}

func decryptCBC(block cipher.Block, dst, src []byte) {
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(dst, src)
}
