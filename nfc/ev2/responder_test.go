package ev2

import (
	"bytes"
	"testing"
)

// pair returns the two halves of one session: what the reader drives and what
// the card answers with, at the same counter and over the same keys.
func pair(t *testing.T) (reader, card *Session) {
	t.Helper()
	ti := []byte{0x7A, 0x21, 0x08, 0x5E}
	encKey := mustHex(t, "1309C877509E5A215007FF0ED19CA564")
	macKey := mustHex(t, "4C6626F5E72EA694202139295C7A7FC7")

	reader, err := NewSession(ti, encKey, macKey)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	card, err = NewSession(ti, encKey, macKey)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return reader, card
}

// What one half builds, the other half reads, in every mode and across enough
// commands for the counter to matter.
func TestSessionHalvesAgree(t *testing.T) {
	modes := map[string]CommMode{"plain": CommPlain, "mac": CommMAC, "full": CommFull}
	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			reader, card := pair(t)

			for i := range 4 {
				header := []byte{0x02, byte(i), 0x00, 0x00}
				sent := []byte{byte(i), 0xAA, 0xBB}

				cmd, err := reader.Command(0x3D, header, sent, mode)
				if err != nil {
					t.Fatalf("Command: %v", err)
				}

				gotHeader, gotData, err := card.VerifyCommand(cmd, mode, len(header))
				if err != nil {
					t.Fatalf("VerifyCommand: %v", err)
				}
				if !bytes.Equal(gotHeader, header) {
					t.Errorf("header = % X, want % X", gotHeader, header)
				}
				if !bytes.Equal(gotData, sent) {
					t.Errorf("data = % X, want % X", gotData, sent)
				}

				answered := []byte{0xC0, 0xFF, 0xEE, byte(i)}
				resp, err := card.Answer(0x00, answered, mode)
				if err != nil {
					t.Fatalf("Answer: %v", err)
				}

				got, err := reader.Response(resp, mode)
				if err != nil {
					t.Fatalf("Response: %v", err)
				}
				if !bytes.Equal(got, answered) {
					t.Errorf("response data = % X, want % X", got, answered)
				}

				if reader.Counter() != card.Counter() {
					t.Fatalf("counters diverged: reader %d, card %d", reader.Counter(), card.Counter())
				}
			}
		})
	}
}

// The counter is in every MAC, so a command replayed at a later point in the
// session no longer verifies.
func TestVerifyCommandRefusesAReplay(t *testing.T) {
	reader, card := pair(t)

	first, err := reader.Command(0xBD, []byte{0x02}, nil, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if _, _, err := card.VerifyCommand(first, CommMAC, 1); err != nil {
		t.Fatalf("VerifyCommand: %v", err)
	}
	if _, err := card.Answer(0x00, nil, CommMAC); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	if _, _, err := card.VerifyCommand(first, CommMAC, 1); err == nil {
		t.Error("the same command verified twice in one session")
	}
}

// A command whose bytes were changed on the way does not verify.
func TestVerifyCommandRefusesATamperedCommand(t *testing.T) {
	reader, card := pair(t)

	cmd, err := reader.Command(0x3D, []byte{0x02}, []byte{0x01, 0x02}, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	cmd[6] ^= 0xFF

	if _, _, err := card.VerifyCommand(cmd, CommMAC, 1); err == nil {
		t.Error("a tampered command verified")
	}
}

// Both sides derive the session keys from the same two random numbers. The
// authenticator's own derivation is pinned to AN12196 Table 6, so agreeing with
// it pins this one to the same transcript.
func TestDeriveSessionKeysMatchesTheAuthenticator(t *testing.T) {
	auth, err := NewAuthenticator(AuthFirst, 0x00, zeroKey, nil)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	rndA := mustHex(t, "13C5DB8A5930439FC3DEF9A4C675360F")
	auth.SetRandom(&fixedRandom{rndA})

	challenge := mustHex(t, "A04C124213C186F22399D33AC2A3021591AF")
	if _, err := auth.Challenge(challenge); err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	session, err := auth.Finish(mustHex(t, "3FA64DB5446D1F34CD6EA311167F5E4985B89690C04A05F17FA7AB2F081206639100"))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantEnc, wantMAC := session.Keys()

	// The card's random number, as the authenticator recovered it.
	block, err := NewCipher(zeroKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	rndB := make([]byte, randomSize)
	decryptCBC(block, rndB, challenge[:randomSize])

	encKey, macKey, err := DeriveSessionKeys(zeroKey, rndA, rndB)
	if err != nil {
		t.Fatalf("DeriveSessionKeys: %v", err)
	}
	if !bytes.Equal(encKey, wantEnc) {
		t.Errorf("encKey = %X, want %X", encKey, wantEnc)
	}
	if !bytes.Equal(macKey, wantMAC) {
		t.Errorf("macKey = %X, want %X", macKey, wantMAC)
	}
}
