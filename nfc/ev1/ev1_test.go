package ev1

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// fixedRandom stands in for this side's random number so an exchange can be
// reproduced.
type fixedRandom struct{ value []byte }

func (f *fixedRandom) Read(p []byte) (int, error) { return copy(p, f.value), nil }

// The exchange, against ciphertexts computed outside this package with
// openssl enc -aes-128-cbc -nopad, so the test is not this code agreeing with
// itself.
//
//	key   000102030405060708090A0B0C0D0E0F
//	RndB  0123456789ABCDEF0123456789ABCDEF   enciphered from a zero chain
//	RndA  FEDCBA9876543210FEDCBA9876543210
//
// The chain runs through the whole exchange: the card's challenge enciphers
// from zero, our answer enciphers from that ciphertext, and the card's last
// word enciphers from the end of ours.
func TestAuthenticateAESAgainstExternalCiphertexts(t *testing.T) {
	const (
		key      = "000102030405060708090A0B0C0D0E0F"
		rndA     = "FEDCBA9876543210FEDCBA9876543210"
		encRndB  = "3071708FFD2412229B2677BA5F1C52D2"
		encToken = "A36D3BD1DC0552E2B0888BACC5CD19835B525A9266E4CFF525B2D6A58CE40A3C"
		encRndA  = "1009ACC37CF12B759D0CB4D19E6C161A"
		// RndA[0:4] RndB[0:4] RndA[12:16] RndB[12:16].
		sessionKey = "FEDCBA98012345677654321089ABCDEF"
	)

	auth, err := NewAuthenticator(0x00, mustHex(t, key))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	auth.SetRandom(&fixedRandom{mustHex(t, rndA)})

	if got, want := auth.Command(), mustHex(t, "90AA0000010000"); !bytes.Equal(got, want) {
		t.Errorf("Command() = % X, want % X", got, want)
	}

	second, err := auth.Challenge(append(mustHex(t, encRndB), 0x91, 0xAF))
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	want := append(append([]byte{0x90, 0xAF, 0x00, 0x00, 0x20}, mustHex(t, encToken)...), 0x00)
	if !bytes.Equal(second, want) {
		t.Errorf("Challenge() = % X\n          want % X", second, want)
	}

	session, err := auth.Finish(append(mustHex(t, encRndA), 0x91, 0x00))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := session.Key(); !bytes.Equal(got, mustHex(t, sessionKey)) {
		t.Errorf("session key = %X, want %s", got, sessionKey)
	}
}

// The session key takes a quarter of each random number from the front and a
// quarter from the back, which is what libfreefare's session_key_new copies for
// an AES-128 key.
func TestSessionKeyLayout(t *testing.T) {
	rndA := mustHex(t, "A0A1A2A3A4A5A6A7A8A9AAABACADAEAF")
	rndB := mustHex(t, "B0B1B2B3B4B5B6B7B8B9BABBBCBDBEBF")

	got := SessionKey(rndA, rndB)
	want := mustHex(t, "A0A1A2A3B0B1B2B3ACADAEAFBCBDBEBF")
	if !bytes.Equal(got, want) {
		t.Errorf("SessionKey = %X, want %X", got, want)
	}
}

// A card that answers with the wrong number does not authenticate.
func TestAuthenticateRefusesAWrongAnswer(t *testing.T) {
	auth, err := NewAuthenticator(0x00, make([]byte, 16))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, err := auth.Challenge(append(make([]byte, 16), 0x91, 0xAF)); err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if _, err := auth.Finish(append(make([]byte, 16), 0x91, 0x00)); err != ErrAuthFailed {
		t.Errorf("Finish with a wrong answer = %v, want ErrAuthFailed", err)
	}
}

func TestAuthenticateRefusesStepsOutOfOrder(t *testing.T) {
	auth, err := NewAuthenticator(0x00, make([]byte, 16))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, err := auth.Finish(append(make([]byte, 16), 0x91, 0x00)); err != ErrAuthState {
		t.Errorf("Finish before Challenge = %v, want ErrAuthState", err)
	}
}

// A plain command travels exactly as it would unauthenticated. The MAC is still
// computed, because the card computes it too and the chain has to stay in step.
func TestPlainCommandCarriesNoMACButAdvancesTheChain(t *testing.T) {
	session, err := NewSession(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	before := append([]byte(nil), session.iv...)

	cmd, err := session.Command(0xBD, []byte{0x02, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00}, CommPlain)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	want := mustHex(t, "90BD00000702000000020000"+"00")
	if !bytes.Equal(cmd, want) {
		t.Errorf("Command() = % X, want % X", cmd, want)
	}
	if bytes.Equal(session.iv, before) {
		t.Error("the chain did not advance")
	}
}

// A MACed command carries the leading eight bytes of the CMAC, computed from
// the chain as it stood.
func TestMACedCommandAppendsTheLeadingBytes(t *testing.T) {
	session, err := NewSession(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	block, err := ev2.NewCipher(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	// The MAC the card computes for the same command from the same chain.
	full := ev2.CMACFrom(block, make([]byte, 16), []byte{0x54, 0x0F})

	cmd, err := session.Command(0x54, []byte{0x0F}, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	got := cmd[5 : len(cmd)-1] // between Lc and Le
	want := append([]byte{0x0F}, full[:MACSize]...)
	if !bytes.Equal(got, want) {
		t.Errorf("command body = % X, want % X", got, want)
	}
}

// The card MACs its answer over the data it returns followed by the status it
// returns with, so a status cannot be swapped for another.
func TestResponseMACCoversTheStatus(t *testing.T) {
	session, err := NewSession(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Command(0xBD, []byte{0x02}, CommPlain); err != nil {
		t.Fatalf("Command: %v", err)
	}

	// What the card would send back: two bytes of file data, MACed with the
	// status, at the chain the command left behind.
	block, err := ev2.NewCipher(session.Key())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	body := []byte{0x00, 0x09}
	mac := ev2.CMACFrom(block, session.iv, append(append([]byte(nil), body...), 0x00))[:MACSize]

	response := append(append(append([]byte(nil), body...), mac...), 0x91, 0x00)
	got, err := session.Response(response, CommPlain)
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("Response() = % X, want % X", got, body)
	}
}

// An answer whose MAC does not match is refused, and so is one MACed over a
// different status.
func TestResponseRefusesABadMAC(t *testing.T) {
	newSessionAtFirstCommand := func(t *testing.T) *Session {
		t.Helper()
		s, err := NewSession(make([]byte, 16))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := s.Command(0xBD, []byte{0x02}, CommPlain); err != nil {
			t.Fatalf("Command: %v", err)
		}
		return s
	}

	t.Run("a MAC that is simply wrong", func(t *testing.T) {
		s := newSessionAtFirstCommand(t)
		response := append(append([]byte{0x00, 0x09}, make([]byte, MACSize)...), 0x91, 0x00)
		if _, err := s.Response(response, CommPlain); err != ErrMACMismatch {
			t.Errorf("Response = %v, want ErrMACMismatch", err)
		}
	})

	t.Run("a MAC computed over another status", func(t *testing.T) {
		s := newSessionAtFirstCommand(t)
		block, err := ev2.NewCipher(s.Key())
		if err != nil {
			t.Fatalf("NewCipher: %v", err)
		}
		body := []byte{0x00, 0x09}
		// MACed as though the card had answered 0xAE, sent with 0x00.
		mac := ev2.CMACFrom(block, s.iv, append(append([]byte(nil), body...), 0xAE))[:MACSize]

		response := append(append(append([]byte(nil), body...), mac...), 0x91, 0x00)
		if _, err := s.Response(response, CommPlain); err != ErrMACMismatch {
			t.Errorf("Response = %v, want ErrMACMismatch", err)
		}
	})
}

// Enciphered communication is refused rather than sent unprotected.
func TestEncipheredIsRefused(t *testing.T) {
	session, err := NewSession(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Command(0xBD, nil, CommMode(99)); err != ErrEnciphered {
		t.Errorf("Command in an unimplemented mode = %v, want ErrEnciphered", err)
	}
}
