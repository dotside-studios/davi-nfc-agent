package lrp

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// The vectors below are from AN12304 Rev. 1.1 sections 3.1, 3.3 and 3.4:
// plaintext generation, updated keys, LRICB and CMAC_LRP. an12304_test.go runs
// every vector in sections 3.2 to 3.4.

func TestPlaintextsAgainstAN12304(t *testing.T) {
	key := mustHex(t, "567826B8DA8E768432A9548DBE4AA3A0")
	got := Plaintexts(key)
	if len(got) != 16 {
		t.Fatalf("%d plaintexts, want 16", len(got))
	}
	if want := mustHex(t, "AC20D39F5341FE98DFCA21DA86BA7914"); !bytes.Equal(got[0], want) {
		t.Errorf("plaintext 0 = %X, want %X", got[0], want)
	}
	seen := map[string]bool{}
	for i, p := range got {
		if seen[string(p)] {
			t.Errorf("plaintext %d repeats an earlier one", i)
		}
		seen[string(p)] = true
	}
}

func TestUpdatedKeysAgainstAN12304(t *testing.T) {
	key := mustHex(t, "567826B8DA8E768432A9548DBE4AA3A0")
	got := UpdatedKeys(key)
	want := []string{
		"163D14ED24ED935373568EC521E96CF4",
		"1C519C000208B95A39A65DB058327188",
		"FE30AB50467E61783BFE6B5E0560160E",
	}
	if len(got) < len(want) {
		t.Fatalf("%d updated keys, want at least %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], mustHex(t, want[i])) {
			t.Errorf("updated key %d = %X, want %s", i, got[i], want[i])
		}
	}
}

func TestLRICBAgainstAN12304(t *testing.T) {
	key, err := New(mustHex(t, "E0C4935FF0C254CD2CEF8FDDC32460CF"), UpdateMAC)
	if err != nil {
		t.Fatal(err)
	}
	counter := mustHex(t, "C3315DBF")
	plain := mustHex(t, "012D7F1653CAF6503C6AB0C1010E8CB0")
	want := mustHex(t, "FCBBACAA4F29182464F99DE41085266F")

	got, err := key.Encrypt(counter, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Encrypt = %X, want %X", got, want)
	}
	back, err := key.Decrypt(counter, got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, plain) {
		t.Errorf("Decrypt = %X, want %X", back, plain)
	}
}

func TestCMACAgainstAN12304(t *testing.T) {
	key, err := New(mustHex(t, "8195088CE6C393708EBBE6C7914ECB0B"), UpdateMAC)
	if err != nil {
		t.Fatal(err)
	}
	got := key.CMAC(mustHex(t, "BBD5B85772C7"))
	if want := mustHex(t, "AD8595E0B49C5C0DB18E77355F5AAFF6"); !bytes.Equal(got, want) {
		t.Errorf("CMAC = %X, want %X", got, want)
	}
}

// Shapes the worked example does not reach, checked for what must hold of any
// CMAC rather than against a published value: lengths either side of a block
// boundary all differ, and a message is not its own padding.
func TestCMACLengthShapes(t *testing.T) {
	key, err := New(mustHex(t, "8195088CE6C393708EBBE6C7914ECB0B"), UpdateMAC)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33} {
		msg := bytes.Repeat([]byte{0x5A}, n)
		mac := key.CMAC(msg)
		if len(mac) != BlockSize {
			t.Fatalf("CMAC of %d bytes is %d long", n, len(mac))
		}
		if prev, dup := seen[string(mac)]; dup {
			t.Errorf("CMAC of %d bytes equals that of %d", n, prev)
		}
		seen[string(mac)] = n
		if !bytes.Equal(mac, key.CMAC(msg)) {
			t.Errorf("CMAC of %d bytes is not deterministic", n)
		}
	}
	if bytes.Equal(key.CMAC([]byte{0x01, 0x80}), key.CMAC([]byte{0x01})) {
		t.Error("a message equals its own padding")
	}
}

func TestLRICBMultiBlockAndCounterCarry(t *testing.T) {
	key, err := New(mustHex(t, "E0C4935FF0C254CD2CEF8FDDC32460CF"), UpdateENC)
	if err != nil {
		t.Fatal(err)
	}
	plain := bytes.Repeat([]byte{0x11}, 3*BlockSize)
	counter := []byte{0x00, 0xFF}

	enc, err := key.Encrypt(counter, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc[:16], enc[16:32]) || bytes.Equal(enc[16:32], enc[32:]) {
		t.Error("identical blocks enciphered alike under a rising counter")
	}
	if !bytes.Equal(counter, []byte{0x00, 0xFF}) {
		t.Error("Encrypt changed the counter it was given")
	}

	// The second block is the first block's encryption under counter+1, carry
	// included.
	second, err := key.Encrypt([]byte{0x01, 0x00}, plain[:16])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, enc[16:32]) {
		t.Errorf("block 1 = %X, want %X", enc[16:32], second)
	}

	dec, err := key.Decrypt(counter, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Errorf("Decrypt = %X", dec)
	}
}

func TestUpdatedKeysSelectDifferentEvaluations(t *testing.T) {
	raw := mustHex(t, "8195088CE6C393708EBBE6C7914ECB0B")
	a, _ := New(raw, UpdateMAC)
	b, _ := New(raw, UpdateENC)
	if bytes.Equal(a.CMAC([]byte{1}), b.CMAC([]byte{1})) {
		t.Error("two updated keys MAC alike")
	}
}

func TestErrors(t *testing.T) {
	if _, err := New(make([]byte, 15), UpdateMAC); !errors.Is(err, ErrKeySize) {
		t.Errorf("short key: %v", err)
	}
	if _, err := New(make([]byte, 16), 7); err == nil {
		t.Error("a missing updated key was accepted")
	}
	key, _ := New(make([]byte, 16), UpdateENC)
	if _, err := key.Encrypt(nil, make([]byte, 16)); err == nil {
		t.Error("empty counter was accepted")
	}
	if _, err := key.Encrypt([]byte{0}, make([]byte, 15)); err == nil {
		t.Error("a partial block was accepted")
	}
	if _, err := key.Decrypt([]byte{0}, make([]byte, 17)); err == nil {
		t.Error("a partial block was accepted")
	}
}
