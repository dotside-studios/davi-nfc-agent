package ev2

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"

	"github.com/dotside-studios/davi-nfc-agent/nfc/lrp"
)

// Authentication on a card in LRP mode: AuthenticateLRPFirst and
// AuthenticateLRPNonFirst, which share their instruction bytes with the AES
// pair and are told apart by the capabilities the reader declares.
//
// Where AES encrypts the random numbers, LRP sends them in the clear and proves
// the key with MACs, so the exchange has the same two round trips but different
// contents. The reader's second message is its random number and a MAC over both
// numbers; the card answers with a MAC of its own, and on the first
// authentication the transaction identifier and capabilities, enciphered.

const (
	// lrpAuthMode opens the card's first answer, ahead of RndB.
	lrpAuthMode = 0x01

	// lrpCapLen and lrpPCDCap2 are what the reader declares in the command: six
	// bytes of capabilities, the first of which selects LRP.
	lrpCapLen = 6
)

// lrpPCDCap2 is the capability bytes the reader sends, bit 1 of the first byte
// asking for LRP.
var lrpPCDCap2 = [lrpCapLen]byte{0x02}

// lrpFirstReplySize is the card's last answer to AuthenticateLRPFirst: one block
// enciphering the transaction identifier and both sides' capabilities, then a
// MAC over it.
const (
	lrpFirstReplySize    = BlockSize + BlockSize
	lrpNonFirstReplySize = BlockSize
	lrpCaptureSize       = TISize + 2*lrpCapLen // TI, PDcap2, PCDcap2
)

// lrpSessionVectorSuffix closes the session vector, where the AES one has none.
var lrpSessionVectorSuffix = []byte{0x96, 0x69}

// DeriveLRPSessionKey derives the session's master key from the key the
// authentication proved and both sides' random numbers. The MAC and encryption
// keys are the two updated keys of it, which [NewLRPSession] takes.
//
// [LRPAuthenticator] does this itself; it is exported for the card's side of the
// exchange, which has to arrive at the same key.
func DeriveLRPSessionKey(key, rndA, rndB []byte) ([]byte, error) {
	k, err := lrp.New(key, lrp.UpdateMAC)
	if err != nil {
		return nil, err
	}
	if len(rndA) != randomSize || len(rndB) != randomSize {
		return nil, fmt.Errorf("ev2: random numbers are %d and %d bytes, want %d each",
			len(rndA), len(rndB), randomSize)
	}
	sv := make([]byte, 0, 32)
	sv = append(sv, 0x00, 0x01, 0x00, 0x80)
	sv = append(sv, rndA[0:2]...)
	for i := 0; i < 6; i++ {
		sv = append(sv, rndA[2+i]^rndB[i])
	}
	sv = append(sv, rndB[6:16]...)
	sv = append(sv, rndA[8:16]...)
	sv = append(sv, lrpSessionVectorSuffix...)
	return k.CMAC(sv), nil
}

// LRPAuthenticator drives one LRP authentication exchange, in two round trips:
// Command then Challenge, each answer handed to the next step, ending at Finish.
type LRPAuthenticator struct {
	mode   AuthMode
	keyNo  byte
	key    []byte
	random io.Reader

	ti      []byte
	counter uint16

	rndA, rndB []byte
	session    *LRPSession
	stage      int
}

// NewLRPAuthenticator prepares an exchange with the numbered key. For
// AuthNonFirst pass the transaction identifier from the first authentication;
// for AuthFirst pass nil, since the card assigns one.
func NewLRPAuthenticator(mode AuthMode, keyNo byte, key, ti []byte) (*LRPAuthenticator, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w, got %d", ErrKeySize, len(key))
	}
	if mode == AuthNonFirst && len(ti) != TISize {
		return nil, fmt.Errorf("ev2: AuthNonFirst needs the transaction identifier from the first authentication")
	}
	return &LRPAuthenticator{
		mode:   mode,
		keyNo:  keyNo,
		key:    append([]byte(nil), key...),
		random: rand.Reader,
		ti:     append([]byte(nil), ti...),
	}, nil
}

// SetCounter sets the command counter an AuthNonFirst session continues from.
func (a *LRPAuthenticator) SetCounter(counter uint16) { a.counter = counter }

// SetRandom replaces the source of this side's random number, so a test can
// reproduce an exchange. Nothing outside a test should call this.
func (a *LRPAuthenticator) SetRandom(r io.Reader) { a.random = r }

// Command is the first APDU to send.
func (a *LRPAuthenticator) Command() []byte {
	ins := byte(insAuthFirst)
	if a.mode == AuthNonFirst {
		ins = insAuthNonFirst
	}
	data := append([]byte{a.keyNo, lrpCapLen}, lrpPCDCap2[:]...)
	return WrapAPDU(ins, data)
}

// Challenge consumes the card's answer to Command, which is its random number
// behind an authentication mode byte, and returns the second APDU: ours, with a
// MAC over both.
func (a *LRPAuthenticator) Challenge(response []byte) ([]byte, error) {
	if a.stage != 0 {
		return nil, ErrAuthState
	}
	data, err := frameData(response, 1+randomSize)
	if err != nil {
		return nil, err
	}
	if data[0] != lrpAuthMode {
		return nil, fmt.Errorf("ev2: card answered authentication mode %#02x, want LRP (%#02x)", data[0], lrpAuthMode)
	}
	a.rndB = append([]byte(nil), data[1:]...)

	a.rndA = make([]byte, randomSize)
	if _, err := io.ReadFull(a.random, a.rndA); err != nil {
		return nil, fmt.Errorf("ev2: reading a random number: %w", err)
	}

	master, err := DeriveLRPSessionKey(a.key, a.rndA, a.rndB)
	if err != nil {
		return nil, err
	}
	macKey, err := lrp.New(master, lrp.UpdateMAC)
	if err != nil {
		return nil, err
	}

	ti := a.ti
	if a.mode == AuthNonFirst {
		// AuthenticateLRPNonFirst enciphers nothing, so secure messaging starts
		// the counter at zero; after AuthenticateLRPFirst zero is spent.
		a.session, err = NewLRPSession(ti, master, a.counter, 0)
		if err != nil {
			return nil, err
		}
	} else {
		// The transaction identifier is not known until the card's answer.
		a.session, err = NewLRPSession(make([]byte, TISize), master, 0, 1)
		if err != nil {
			return nil, err
		}
	}

	payload := append(append([]byte(nil), a.rndA...), macKey.CMAC(append(append([]byte(nil), a.rndA...), a.rndB...))...)
	a.stage = 1
	return WrapAPDU(insAdditionalFrame, payload), nil
}

// Finish consumes the card's answer to Challenge and returns the session. The
// card's MAC could only come from a card that holds the key.
func (a *LRPAuthenticator) Finish(response []byte) (*LRPSession, error) {
	if a.stage != 1 {
		return nil, ErrAuthState
	}
	size := lrpNonFirstReplySize
	if a.mode == AuthFirst {
		size = lrpFirstReplySize
	}
	data, err := frameData(response, size)
	if err != nil {
		return nil, err
	}

	s := a.session
	if a.mode == AuthNonFirst {
		if subtle.ConstantTimeCompare(s.macKey.CMAC(lrpPICCProof(a.rndB, a.rndA, nil)), data) != 1 {
			return nil, ErrAuthFailed
		}
		a.stage = 2
		return s, nil
	}

	enc, mac := data[:BlockSize], data[BlockSize:]
	if subtle.ConstantTimeCompare(s.macKey.CMAC(lrpPICCProof(a.rndB, a.rndA, enc)), mac) != 1 {
		return nil, ErrAuthFailed
	}
	plain, err := s.encKey.Decrypt(lrpCounterBytes(0), enc)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(plain[TISize+lrpCapLen:], lrpPCDCap2[:]) {
		return nil, ErrAuthFailed
	}
	s.ti = append([]byte(nil), plain[:TISize]...)
	a.stage = 2
	return s, nil
}

// LRPAnswer is the card's side of an LRP authentication: given the key, the
// random number it offered and the reader's second message, it verifies the
// reader and returns the card's answer and the session. For a card that
// continues a transaction (first false) ti and counter are the open session's.
//
// It exists for an emulator, and to prove the two sides agree.
func LRPAnswer(first bool, key, rndB, payload, ti []byte, counter uint16) (reply []byte, s *LRPSession, err error) {
	if len(payload) != randomSize+BlockSize || len(rndB) != randomSize {
		return nil, nil, fmt.Errorf("ev2: LRP authentication message is %d bytes, want %d", len(payload), randomSize+BlockSize)
	}
	rndA, proof := payload[:randomSize], payload[randomSize:]

	master, err := DeriveLRPSessionKey(key, rndA, rndB)
	if err != nil {
		return nil, nil, err
	}
	macKey, err := lrp.New(master, lrp.UpdateMAC)
	if err != nil {
		return nil, nil, err
	}
	if subtle.ConstantTimeCompare(macKey.CMAC(append(append([]byte(nil), rndA...), rndB...)), proof) != 1 {
		return nil, nil, ErrAuthFailed
	}

	if !first {
		s, err = NewLRPSession(ti, master, counter, 0)
		if err != nil {
			return nil, nil, err
		}
		return s.macKey.CMAC(lrpPICCProof(rndB, rndA, nil)), s, nil
	}

	s, err = NewLRPSession(ti, master, 0, 1)
	if err != nil {
		return nil, nil, err
	}
	capture := make([]byte, 0, lrpCaptureSize)
	capture = append(capture, ti...)
	capture = append(capture, lrpPCDCap2[:]...) // PDcap2
	capture = append(capture, lrpPCDCap2[:]...)
	enc, err := s.encKey.Encrypt(lrpCounterBytes(0), capture)
	if err != nil {
		return nil, nil, err
	}
	return append(enc, s.macKey.CMAC(lrpPICCProof(rndB, rndA, enc))...), s, nil
}

// lrpPICCProof is what the card's MAC covers: both random numbers, the card's
// first, then on AuthenticateLRPFirst the enciphered capture.
func lrpPICCProof(rndB, rndA, picc []byte) []byte {
	out := make([]byte, 0, 2*randomSize+len(picc))
	out = append(out, rndB...)
	out = append(out, rndA...)
	return append(out, picc...)
}
