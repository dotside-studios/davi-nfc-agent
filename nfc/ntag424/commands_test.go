package ntag424

import (
	"bytes"
	"errors"
	"testing"
)

// AN12196 Table 18: ChangeFileSettings turning SDM on for the NDEF file, sent as
// the second command of Table 14's session, so its IV and MAC are the ones for
// command number 1.
func TestChangeFileSettingsAN12196Table18(t *testing.T) {
	s := tableSession(t, "9D00C4DF",
		"1309C877509E5A215007FF0ED19CA564",
		"4C6626F5E72EA694202139295C7A7FC7",
	)
	s.counter = 1 // Table 17's WriteData was the session's first command.

	settings := mustHex(t, "4000E0C1F121200000430000430000")
	cmd, err := ChangeFileSettings(s, NDEFFileNo, settings)
	if err != nil {
		t.Fatalf("ChangeFileSettings: %v", err)
	}
	want := mustHex(t, "905F0000190261B6D97903566E84C3AE5274467E89EAD799B7C1A0EF7A0400")
	if !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}

	if _, err := s.Response(mustHex(t, "57BFF87B1241E93D9100"), CommFull); err != nil {
		t.Fatalf("Response: %v", err)
	}
}

// FileSettings must produce the settings block Table 18 sends: SDM on, UID and
// counter mirrored as text, PICCData encrypted under key 2, the MAC under key 1.
func TestFileSettingsEncodeAN12196Table18(t *testing.T) {
	got, err := FileSettings{
		SDMEnabled:        true,
		CommMode:          CommPlain,
		ReadWrite:         0x00,
		Change:            0x00,
		Read:              AccessFree,
		Write:             0x00,
		MirrorUID:         true,
		MirrorReadCounter: true,
		ASCIIEncoding:     true,
		SDMCounterRet:     0x01,
		SDMMetaRead:       0x02,
		SDMFileRead:       0x01,
		PICCDataOffset:    0x20,
		MACInputOffset:    0x43,
		MACOffset:         0x43,
	}.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := mustHex(t, "4000E0C1F121200000430000430000"); !bytes.Equal(got, want) {
		t.Errorf("settings = %X, want %X", got, want)
	}
}

// AN12196 Table 25: changing a key that is not the session's, sent as the
// difference from the old key plus a checksum over the new one.
func TestChangeKeyOtherThanSessionsAN12196Table25(t *testing.T) {
	s := tableSession(t, "7614281A",
		"4CF3CB41A22583A61E89B158D252FC53",
		"5529860B2FC5FB6154B7F28361D30BF9",
	)
	s.counter = 2

	cmd, err := ChangeKey(s,
		0x02, 0x00,
		mustHex(t, "00000000000000000000000000000000"),
		mustHex(t, "F3847D627727ED3BC9C4CC050489B966"),
		0x01,
	)
	if err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}
	want := mustHex(t, "90C4000029022CF362B7BF4311FF3BE1DAA295E8C68DE09050560D19B9E16C2393AE9CD1FAC75D0CE20BCD1D06E600")
	if !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}
}

// That checksum is CRC-32 without the final inversion. Pinned on its own,
// because the card refuses a wrong one without saying why.
func TestKeyCRCAN12196Table25(t *testing.T) {
	got := keyCRC(mustHex(t, "F3847D627727ED3BC9C4CC050489B966"))
	if want := mustHex(t, "789DFADC"); !bytes.Equal(got, want) {
		t.Errorf("CRC = %X, want %X", got, want)
	}
}

// AN12196 Table 26: changing the session's own key, which is sent directly
// because authenticating with it already proved the caller holds it.
func TestChangeKeyOwnAN12196Table26(t *testing.T) {
	s := tableSession(t, "7614281A",
		"4CF3CB41A22583A61E89B158D252FC53",
		"5529860B2FC5FB6154B7F28361D30BF9",
	)
	s.counter = 3

	cmd, err := ChangeKey(s, 0x00, 0x00, nil, mustHex(t, "5004BF991F408672B1EF00F08F9E8647"), 0x01)
	if err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}
	want := mustHex(t, "90C400002900C0EB4DEEFEDDF0B513A03A95A75491818580503190D4D05053FF75668A01D6FDA6610234BDED643200")
	if !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}
}

// AN12196 Table 28: GetCardUID carries no header and no data, so its MAC covers
// the instruction, the counter and the transaction alone.
func TestGetCardUIDAN12196Table28(t *testing.T) {
	s := tableSession(t, "DF055522",
		"2B4D963C014DC36F24F69A50A394F875",
		"379D32130CE61705DD5FD8C36B95D764",
	)

	cmd, err := GetCardUID(s)
	if err != nil {
		t.Fatalf("GetCardUID: %v", err)
	}
	if want := mustHex(t, "90510000088E2C155ADDA99BE300"); !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}

	uid, err := ParseCardUID(s, mustHex(t, "70756055688505B52A5E26E59E329CD6595F672298EA41B79100"))
	if err != nil {
		t.Fatalf("ParseCardUID: %v", err)
	}
	if want := mustHex(t, "04958CAA5C5E80"); !bytes.Equal(uid, want) {
		t.Errorf("UID = %X, want %X", uid, want)
	}
}

// A key change that cannot be built is refused rather than sent wrong. The old
// key has no default: without it the card cannot check the change.
func TestChangeKeyRefusesWhatItCannotBuild(t *testing.T) {
	s := tableSession(t, "7614281A", zeroHex, zeroHex)

	t.Run("no session", func(t *testing.T) {
		if _, err := ChangeKey(nil, 0, 0, nil, make([]byte, KeySize), 1); err == nil {
			t.Error("a key change was built without a session")
		}
	})

	t.Run("new key of the wrong size", func(t *testing.T) {
		if _, err := ChangeKey(s, 0, 0, nil, make([]byte, 8), 1); !errors.Is(err, ErrKeySize) {
			t.Errorf("err = %v, want ErrKeySize", err)
		}
	})

	t.Run("another key, without the old one", func(t *testing.T) {
		if _, err := ChangeKey(s, 0x02, 0x00, nil, make([]byte, KeySize), 1); !errors.Is(err, ErrKeySize) {
			t.Errorf("err = %v, want ErrKeySize", err)
		}
	})
}

// The two key-change forms are different commands. The card refuses the wrong
// one, which a caller cannot tell from a wrong key.
func TestChangeKeyFormDependsOnTheSessionsKey(t *testing.T) {
	newKey := mustHex(t, "5004BF991F408672B1EF00F08F9E8647")

	own := tableSession(t, "7614281A", zeroHex, zeroHex)
	sameKey, err := ChangeKey(own, 0x00, 0x00, nil, newKey, 0x01)
	if err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}

	other := tableSession(t, "7614281A", zeroHex, zeroHex)
	otherKey, err := ChangeKey(other, 0x00, 0x01, zeroKey, newKey, 0x01)
	if err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}

	if bytes.Equal(sameKey, otherKey) {
		t.Error("both forms produced the same command")
	}
	// Both encrypt to the same length: 17 and 21 bytes pad to two blocks alike,
	// so the wrong form cannot be spotted by its size.
	if len(sameKey) != len(otherKey) {
		t.Errorf("the two forms are %d and %d bytes; the test's premise has changed", len(sameKey), len(otherKey))
	}
}

// Settings that cannot be encoded are refused rather than written wrong.
func TestFileSettingsRefusesImpossibleRights(t *testing.T) {
	_, err := FileSettings{SDMEnabled: true, Read: 0x40}.Encode()
	if err == nil {
		t.Error("an access right of 0x40 was accepted")
	}
}
