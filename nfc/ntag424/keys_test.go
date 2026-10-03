package ntag424

import (
	"bytes"
	"testing"
)

// AN10922 section 2.2.1: AES-128 diversification, with a 7-byte UID and an
// application identifier plus system identifier as the diversification data.
func TestDiversifyKeyAN10922(t *testing.T) {
	master := mustHex(t, "00112233445566778899AABBCCDDEEFF")
	uid := mustHex(t, "04782E21801D80")
	systemID := mustHex(t, "3042F54E585020416275")

	got, err := DiversifyKey(master, uid, systemID)
	if err != nil {
		t.Fatalf("DiversifyKey: %v", err)
	}
	if want := mustHex(t, "A8DD63A3B89D54B37CA802473FDA9175"); !bytes.Equal(got, want) {
		t.Errorf("key = %X, want %X", got, want)
	}
}

func TestDiversifyKeyRejectsBadInput(t *testing.T) {
	master := make([]byte, KeySize)
	if _, err := DiversifyKey(master[:8], []byte{1}, nil); err == nil {
		t.Error("short master accepted")
	}
	if _, err := DiversifyKey(master, nil, nil); err == nil {
		t.Error("empty UID accepted")
	}
	if _, err := DiversifyKey(master, make([]byte, 7), make([]byte, 25)); err == nil {
		t.Error("input over 32 bytes accepted")
	}
	if _, err := DiversifyKey(master, make([]byte, 7), make([]byte, 24)); err != nil {
		t.Errorf("32-byte input refused: %v", err)
	}
}

func TestKeySetKey(t *testing.T) {
	master := mustHex(t, "00112233445566778899AABBCCDDEEFF")
	explicit := bytes.Repeat([]byte{0xAB}, KeySize)
	uid := mustHex(t, "04782E21801D80")
	systemID := mustHex(t, "3042F54E585020416275")

	plain := KeySet{Master: master, Slots: map[byte][]byte{3: explicit}}
	if got, ok := plain.Key(0, uid); !ok || !bytes.Equal(got, master) {
		t.Errorf("slot 0 = %X, %v, want master", got, ok)
	}
	if got, ok := plain.Key(3, uid); !ok || !bytes.Equal(got, explicit) {
		t.Errorf("slot 3 = %X, %v, want explicit key", got, ok)
	}
	if _, ok := plain.Key(5, uid); ok {
		t.Error("slot 5 answered from the master")
	}

	div := KeySet{Master: master, SystemID: systemID, Diversify: true, Slots: map[byte][]byte{3: explicit}}
	want := mustHex(t, "A8DD63A3B89D54B37CA802473FDA9175")
	if got, ok := div.Key(1, uid); !ok || !bytes.Equal(got, want) {
		t.Errorf("diversified slot 1 = %X, %v, want %X", got, ok, want)
	}
	if got, ok := div.Key(3, uid); !ok || !bytes.Equal(got, explicit) {
		t.Errorf("explicit slot was diversified: %X", got)
	}
	if _, ok := div.Key(1, nil); ok {
		t.Error("diversified key returned without a UID")
	}

	got, _ := plain.Key(0, uid)
	got[0] ^= 0xFF
	if again, _ := plain.Key(0, uid); !bytes.Equal(again, master) {
		t.Error("Key returned the set's own memory")
	}
}

func TestKeySetCopyAndEmpty(t *testing.T) {
	if !(KeySet{}).Empty() {
		t.Error("zero KeySet is not empty")
	}
	orig := KeySet{
		Master:   bytes.Repeat([]byte{1}, KeySize),
		SystemID: []byte{9},
		Slots:    map[byte][]byte{2: bytes.Repeat([]byte{2}, KeySize)},
	}
	if orig.Empty() {
		t.Error("populated KeySet is empty")
	}
	if (KeySet{Slots: map[byte][]byte{0: orig.Master}}).Empty() {
		t.Error("KeySet with a slot is empty")
	}

	c := orig.Copy()
	c.Master[0] = 0xFF
	c.SystemID[0] = 0xFF
	c.Slots[2][0] = 0xFF
	c.Slots[4] = nil
	if orig.Master[0] != 1 || orig.SystemID[0] != 9 || orig.Slots[2][0] != 2 || len(orig.Slots) != 1 {
		t.Errorf("Copy shares memory with the original: %+v", orig)
	}
	if z := (KeySet{}).Copy(); z.Master != nil || z.Slots != nil || !z.Empty() {
		t.Errorf("zero Copy = %+v", z)
	}
}
