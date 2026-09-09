package ntag424

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// fixedRandom replays a known random number, so a published exchange can be
// reproduced exactly. Authentication is only sound because this number is
// unpredictable, which is why nothing outside a test may set it.
type fixedRandom struct{ b []byte }

func (r *fixedRandom) Read(p []byte) (int, error) {
	return copy(p, r.b), nil
}

// AN12196 Table 14: a complete AuthenticateEV2First with key 0, every
// intermediate value published. Driving it with the note's own RndA reproduces
// the exchange byte for byte, including the two session keys.
func TestAuthenticateFirstAN12196Table14(t *testing.T) {
	auth, err := NewAuthenticator(AuthFirst, 0x00, zeroKey, nil)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	auth.SetRandom(&fixedRandom{mustHex(t, "13C5DB8A5930439FC3DEF9A4C675360F")})

	if got, want := auth.Command(), mustHex(t, "9071000002000000"); !bytes.Equal(got, want) {
		t.Errorf("first command = %X, want %X", got, want)
	}

	second, err := auth.Challenge(mustHex(t, "A04C124213C186F22399D33AC2A3021591AF"))
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	want := mustHex(t, "90AF00002035C3E05A752E0144BAC0DE51C1F22C56B34408A23D8AEA266CAB947EA8E0118D00")
	if !bytes.Equal(second, want) {
		t.Errorf("second command = %X, want %X", second, want)
	}

	session, err := auth.Finish(mustHex(t, "3FA64DB5446D1F34CD6EA311167F5E4985B89690C04A05F17FA7AB2F081206639100"))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got, want := session.TI(), mustHex(t, "9D00C4DF"); !bytes.Equal(got, want) {
		t.Errorf("TI = %X, want %X", got, want)
	}
	encKey, macKey := session.Keys()
	if want := mustHex(t, "1309C877509E5A215007FF0ED19CA564"); !bytes.Equal(encKey, want) {
		t.Errorf("KSesAuthENC = %X, want %X", encKey, want)
	}
	if want := mustHex(t, "4C6626F5E72EA694202139295C7A7FC7"); !bytes.Equal(macKey, want) {
		t.Errorf("KSesAuthMAC = %X, want %X", macKey, want)
	}
	if session.Counter() != 0 {
		t.Errorf("a fresh session's counter is %d, want 0", session.Counter())
	}
}

// AN12196 Table 23: AuthenticateEV2NonFirst, which sends a shorter command and
// is answered with our random number alone, no transaction identifier. The one
// from the first authentication carries over.
func TestAuthenticateNonFirstAN12196Table23(t *testing.T) {
	ti := mustHex(t, "9D00C4DF")
	auth, err := NewAuthenticator(AuthNonFirst, 0x00, zeroKey, ti)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	auth.SetRandom(&fixedRandom{mustHex(t, "60BE759EDA560250AC57CDDC11743CF6")})

	if got, want := auth.Command(), mustHex(t, "90770000010000"); !bytes.Equal(got, want) {
		t.Errorf("first command = %X, want %X", got, want)
	}

	second, err := auth.Challenge(mustHex(t, "A6A2B3C572D06C097BB8DB70463E22DC91AF"))
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	want := mustHex(t, "90AF000020BE7D45753F2CAB85F34BC60CE58B940763FE969658A532DF6D95EA2773F6E99100")
	if !bytes.Equal(second, want) {
		t.Errorf("second command = %X, want %X", second, want)
	}

	session, err := auth.Finish(mustHex(t, "B888349C24B315EAB5B589E279C8263E9100"))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := session.TI(); !bytes.Equal(got, ti) {
		t.Errorf("TI = %X, want the one carried in, %X", got, ti)
	}
	encKey, macKey := session.Keys()
	if want := mustHex(t, "4CF3CB41A22583A61E89B158D252FC53"); !bytes.Equal(encKey, want) {
		t.Errorf("KSesAuthENC = %X, want %X", encKey, want)
	}
	if want := mustHex(t, "5529860B2FC5FB6154B7F28361D30BF9"); !bytes.Equal(macKey, want) {
		t.Errorf("KSesAuthMAC = %X, want %X", macKey, want)
	}
}

// A card that cannot return our random number does not hold the key. This is
// the whole point of the exchange, so it is checked rather than assumed: the
// card's last answer is corrupted and must be refused.
func TestAuthenticateRefusesAWrongAnswer(t *testing.T) {
	newExchange := func(t *testing.T) *Authenticator {
		t.Helper()
		auth, err := NewAuthenticator(AuthFirst, 0x00, zeroKey, nil)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		auth.SetRandom(&fixedRandom{mustHex(t, "13C5DB8A5930439FC3DEF9A4C675360F")})
		if _, err := auth.Challenge(mustHex(t, "A04C124213C186F22399D33AC2A3021591AF")); err != nil {
			t.Fatalf("Challenge: %v", err)
		}
		return auth
	}

	t.Run("a corrupted answer", func(t *testing.T) {
		auth := newExchange(t)
		bad := mustHex(t, "3FA64DB5446D1F34CD6EA311167F5E4985B89690C04A05F17FA7AB2F081206639100")
		bad[0] ^= 0xFF
		if _, err := auth.Finish(bad); !errors.Is(err, ErrAuthFailed) {
			t.Errorf("err = %v, want ErrAuthFailed", err)
		}
	})

	t.Run("another exchange's answer", func(t *testing.T) {
		auth := newExchange(t)
		if _, err := auth.Finish(mustHex(t, "B888349C24B315EAB5B589E279C8263E9100")); err == nil {
			t.Error("an answer from a different exchange was accepted")
		}
	})

	t.Run("a card that refuses", func(t *testing.T) {
		auth := newExchange(t)
		if _, err := auth.Finish(mustHex(t, "911C")); err == nil {
			t.Error("an error status word was accepted")
		}
	})
}

// The steps have an order, and taking them out of it is an error rather than a
// silently wrong session.
func TestAuthenticateRefusesStepsOutOfOrder(t *testing.T) {
	auth, err := NewAuthenticator(AuthFirst, 0x00, zeroKey, nil)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, err := auth.Finish(mustHex(t, "00009100")); !errors.Is(err, ErrAuthState) {
		t.Errorf("Finish before Challenge: err = %v, want ErrAuthState", err)
	}
}

// A session cannot be started with a key of the wrong size, and NonFirst cannot
// be started without the transaction it continues.
func TestNewAuthenticatorRefusesBadArguments(t *testing.T) {
	if _, err := NewAuthenticator(AuthFirst, 0, make([]byte, 8), nil); !errors.Is(err, ErrKeySize) {
		t.Errorf("short key: err = %v, want ErrKeySize", err)
	}
	if _, err := NewAuthenticator(AuthNonFirst, 0, zeroKey, nil); err == nil {
		t.Error("AuthNonFirst was started without a transaction identifier")
	}
}

// By default the random number comes from crypto/rand, so two exchanges with
// the same key never repeat. A fixed one is a test's doing and nothing else's.
func TestAuthenticatorUsesRealRandomnessByDefault(t *testing.T) {
	first, second := "", ""
	for _, out := range []*string{&first, &second} {
		auth, err := NewAuthenticator(AuthFirst, 0x00, zeroKey, nil)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cmd, err := auth.Challenge(mustHex(t, "A04C124213C186F22399D33AC2A3021591AF"))
		if err != nil {
			t.Fatalf("Challenge: %v", err)
		}
		*out = hex.EncodeToString(cmd)
	}
	if first == second {
		t.Error("two exchanges sent the same challenge; the random number is not random")
	}
}
