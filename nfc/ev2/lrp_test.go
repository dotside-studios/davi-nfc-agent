package ev2

import (
	"bytes"
	"errors"
	"testing"
)

// These tests prove the reader's and the card's halves of LRP authentication
// and secure messaging agree with each other, and that tampering is caught. They
// are not pinned to published transcripts: the exchange's layout is built from
// the specification's description, and no NXP worked example of it is pinned
// here. The primitive under it is pinned in package lrp.

type lrpExchange struct {
	reader *LRPSession
	card   *LRPSession
}

func lrpAuthenticate(t *testing.T, mode AuthMode, key, ti []byte, counter uint16) lrpExchange {
	t.Helper()
	a, err := NewLRPAuthenticator(mode, 0, key, ti)
	if err != nil {
		t.Fatal(err)
	}
	a.SetCounter(counter)
	a.SetRandom(bytes.NewReader(bytes.Repeat([]byte{0x3C}, randomSize)))

	cmd := a.Command()
	ins := byte(insAuthFirst)
	if mode == AuthNonFirst {
		ins = insAuthNonFirst
	}
	want := []byte{0x90, ins, 0x00, 0x00, 0x08, 0x00, 0x06, 0x02, 0, 0, 0, 0, 0, 0x00}
	if !bytes.Equal(cmd, want) {
		t.Fatalf("Command = %X, want %X", cmd, want)
	}

	rndB := bytes.Repeat([]byte{0xB1}, randomSize)
	first := append(append([]byte{lrpAuthMode}, rndB...), 0x91, 0xAF)
	second, err := a.Challenge(first)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := unwrapAPDU(second)
	if err != nil {
		t.Fatal(err)
	}

	cardTI := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if mode == AuthNonFirst {
		cardTI = ti
	}
	reply, card, err := LRPAnswer(mode == AuthFirst, key, rndB, payload, cardTI, counter)
	if err != nil {
		t.Fatalf("LRPAnswer: %v", err)
	}
	reader, err := a.Finish(append(reply, 0x91, 0x00))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return lrpExchange{reader: reader, card: card}
}

func TestLRPAuthenticateFirst(t *testing.T) {
	x := lrpAuthenticate(t, AuthFirst, zeroKey, nil, 0)
	if !bytes.Equal(x.reader.TI(), []byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Errorf("TI = %X", x.reader.TI())
	}
	if x.reader.Counter() != 0 || x.card.Counter() != 0 {
		t.Errorf("counters %d, %d, want 0", x.reader.Counter(), x.card.Counter())
	}
}

func TestLRPAuthenticateNonFirstKeepsTransactionAndCounter(t *testing.T) {
	ti := []byte{1, 2, 3, 4}
	x := lrpAuthenticate(t, AuthNonFirst, zeroKey, ti, 9)
	if !bytes.Equal(x.reader.TI(), ti) {
		t.Errorf("TI = %X, want %X", x.reader.TI(), ti)
	}
	if x.reader.Counter() != 9 || x.card.Counter() != 9 {
		t.Errorf("counters %d, %d, want 9", x.reader.Counter(), x.card.Counter())
	}
	roundTrip(t, x, CommFull, []byte("after reauth"))
}

func roundTrip(t *testing.T, x lrpExchange, mode CommMode, data []byte) {
	t.Helper()
	header := []byte{0x02, 0x00, 0x00, 0x00}
	cmd, err := x.reader.Command(0xAD, header, data, mode)
	if err != nil {
		t.Fatal(err)
	}
	gotHeader, gotData, err := x.card.VerifyCommand(cmd, mode, len(header))
	if err != nil {
		t.Fatalf("VerifyCommand: %v", err)
	}
	if !bytes.Equal(gotHeader, header) || !bytes.Equal(gotData, data) {
		t.Fatalf("card read %X|%X, want %X|%X", gotHeader, gotData, header, data)
	}
	if mode == CommFull && len(data) > 0 && bytes.Contains(cmd, data) {
		t.Error("CommFull sent its data in the clear")
	}

	answer := []byte("the card's answer to " + string(data))
	resp, err := x.card.Answer(0x00, answer, mode)
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.reader.Response(resp, mode)
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if !bytes.Equal(got, answer) {
		t.Errorf("reader read %q, want %q", got, answer)
	}
	if x.reader.Counter() != x.card.Counter() {
		t.Errorf("counters diverged: %d and %d", x.reader.Counter(), x.card.Counter())
	}
}

func TestLRPSessionRoundTrips(t *testing.T) {
	x := lrpAuthenticate(t, AuthFirst, zeroKey, nil, 0)
	for i, mode := range []CommMode{CommPlain, CommMAC, CommFull, CommFull, CommMAC, CommFull} {
		data := bytes.Repeat([]byte{byte(0x10 + i)}, 5+i*13)
		roundTrip(t, x, mode, data)
	}
	if x.reader.Counter() != 6 {
		t.Errorf("counter = %d, want 6", x.reader.Counter())
	}
	if x.reader.encCounter != x.card.encCounter {
		t.Errorf("enciphering counters diverged: %d and %d", x.reader.encCounter, x.card.encCounter)
	}
}

func TestLRPSessionRejectsTampering(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		x := lrpAuthenticate(t, AuthFirst, zeroKey, nil, 0)
		cmd, _ := x.reader.Command(0x8D, []byte{2}, []byte("data"), CommFull)
		cmd[6] ^= 0x01
		if _, _, err := x.card.VerifyCommand(cmd, CommFull, 1); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("VerifyCommand = %v, want ErrMACMismatch", err)
		}
	})
	t.Run("response", func(t *testing.T) {
		x := lrpAuthenticate(t, AuthFirst, zeroKey, nil, 0)
		cmd, _ := x.reader.Command(0xAD, []byte{2}, nil, CommFull)
		if _, _, err := x.card.VerifyCommand(cmd, CommFull, 1); err != nil {
			t.Fatal(err)
		}
		resp, _ := x.card.Answer(0x00, []byte("secret"), CommFull)
		resp[0] ^= 0x01
		if _, err := x.reader.Response(resp, CommFull); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("Response = %v, want ErrMACMismatch", err)
		}
		if x.reader.Counter() != 0 {
			t.Error("the counter advanced on a response that did not verify")
		}
	})
	t.Run("replayed command", func(t *testing.T) {
		x := lrpAuthenticate(t, AuthFirst, zeroKey, nil, 0)
		cmd, _ := x.reader.Command(0xAD, []byte{2}, nil, CommMAC)
		_, _, _ = x.card.VerifyCommand(cmd, CommMAC, 1)
		resp, _ := x.card.Answer(0x00, nil, CommMAC)
		if _, err := x.reader.Response(resp, CommMAC); err != nil {
			t.Fatal(err)
		}
		if _, _, err := x.card.VerifyCommand(cmd, CommMAC, 1); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("replay = %v, want ErrMACMismatch", err)
		}
	})
}

func TestLRPAuthenticationFailures(t *testing.T) {
	rndB := bytes.Repeat([]byte{0xB1}, randomSize)

	t.Run("wrong key", func(t *testing.T) {
		a, _ := NewLRPAuthenticator(AuthFirst, 0, zeroKey, nil)
		second, err := a.Challenge(append(append([]byte{lrpAuthMode}, rndB...), 0x91, 0xAF))
		if err != nil {
			t.Fatal(err)
		}
		_, payload, _ := unwrapAPDU(second)
		other := bytes.Repeat([]byte{0x01}, KeySize)
		if _, _, err := LRPAnswer(true, other, rndB, payload, []byte{1, 2, 3, 4}, 0); !errors.Is(err, ErrAuthFailed) {
			t.Errorf("LRPAnswer = %v, want ErrAuthFailed", err)
		}
	})
	t.Run("card holding another key", func(t *testing.T) {
		a, _ := NewLRPAuthenticator(AuthFirst, 0, zeroKey, nil)
		second, _ := a.Challenge(append(append([]byte{lrpAuthMode}, rndB...), 0x91, 0xAF))
		_, payload, _ := unwrapAPDU(second)
		reply, _, err := LRPAnswer(true, zeroKey, rndB, payload, []byte{1, 2, 3, 4}, 0)
		if err != nil {
			t.Fatal(err)
		}
		reply[len(reply)-1] ^= 0x01
		if _, err := a.Finish(append(reply, 0x91, 0x00)); !errors.Is(err, ErrAuthFailed) {
			t.Errorf("Finish = %v, want ErrAuthFailed", err)
		}
	})
	t.Run("AES answer", func(t *testing.T) {
		a, _ := NewLRPAuthenticator(AuthFirst, 0, zeroKey, nil)
		if _, err := a.Challenge(append(make([]byte, 16), 0x91, 0xAF)); err == nil {
			t.Error("an AES-mode answer was accepted")
		}
	})
	t.Run("wrong mode byte", func(t *testing.T) {
		a, _ := NewLRPAuthenticator(AuthFirst, 0, zeroKey, nil)
		if _, err := a.Challenge(append(append([]byte{0x02}, rndB...), 0x91, 0xAF)); err == nil {
			t.Error("an unknown authentication mode was accepted")
		}
	})
	t.Run("out of order", func(t *testing.T) {
		a, _ := NewLRPAuthenticator(AuthFirst, 0, zeroKey, nil)
		if _, err := a.Finish(make([]byte, 34)); !errors.Is(err, ErrAuthState) {
			t.Errorf("Finish first = %v, want ErrAuthState", err)
		}
	})
	t.Run("bad inputs", func(t *testing.T) {
		if _, err := NewLRPAuthenticator(AuthFirst, 0, make([]byte, 5), nil); !errors.Is(err, ErrKeySize) {
			t.Errorf("short key: %v", err)
		}
		if _, err := NewLRPAuthenticator(AuthNonFirst, 0, zeroKey, nil); err == nil {
			t.Error("NonFirst without a transaction identifier was accepted")
		}
		if _, err := NewLRPSession([]byte{1}, zeroKey, 0, 0); err == nil {
			t.Error("a short transaction identifier was accepted")
		}
	})
}
