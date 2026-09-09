package ntag424

import (
	"bytes"
	"errors"
	"testing"
)

// Every case below is a worked example from NXP's AN12196 rev 2.0, which is the
// only way to know this implementation agrees with the tags: the algorithms are
// keyed hashes, so a wrong one produces plausible-looking bytes that no tag will
// ever match. The table numbers name where each vector comes from.

// zeroKey is the factory-default key the application note's examples use.
var zeroKey = make([]byte, KeySize)

// AN12196 Table 1. The only example with a key that is not all zeros, so it is
// the one that would catch a derivation that ignores the key.
func TestSessionKeysAN12196Table1(t *testing.T) {
	data := &PICCData{
		UID:             mustHex(t, "04C767F2066180"),
		ReadCounter:     1, // 010000 least significant byte first
		UIDMirrored:     true,
		CounterMirrored: true,
	}

	if got, want := sessionVector(sv1Prefix, data), mustHex(t, "C33C0001008004C767F2066180010000"); !bytes.Equal(got, want) {
		t.Errorf("SV1 = %X, want %X", got, want)
	}
	if got, want := sessionVector(sv2Prefix, data), mustHex(t, "3CC30001008004C767F2066180010000"); !bytes.Equal(got, want) {
		t.Errorf("SV2 = %X, want %X", got, want)
	}

	encKey, macKey, err := SessionKeys(mustHex(t, "5ACE7E50AB65D5D51FD5BF5A16B8205B"), data)
	if err != nil {
		t.Fatalf("SessionKeys: %v", err)
	}
	if want := mustHex(t, "66DA61797E23DECA5D8ECA13BBADF7A9"); !bytes.Equal(encKey, want) {
		t.Errorf("KSesSDMFileReadENC = %X, want %X", encKey, want)
	}
	if want := mustHex(t, "3A3E8110E05311F7A3FCF0D969BF2B48"); !bytes.Equal(macKey, want) {
		t.Errorf("KSesSDMFileReadMAC = %X, want %X", macKey, want)
	}
}

// AN12196 Table 2.
func TestDecryptPICCDataAN12196Table2(t *testing.T) {
	data, err := DecryptPICCData(zeroKey, mustHex(t, "EF963FF7828658A599F3041510671E88"))
	if err != nil {
		t.Fatalf("DecryptPICCData: %v", err)
	}

	if want := mustHex(t, "04DE5F1EACC040"); !bytes.Equal(data.UID, want) {
		t.Errorf("UID = %X, want %X", data.UID, want)
	}
	if data.ReadCounter != 0x3D {
		t.Errorf("ReadCounter = %d, want %d", data.ReadCounter, 0x3D)
	}
	if !data.UIDMirrored || !data.CounterMirrored {
		t.Errorf("UIDMirrored = %v, CounterMirrored = %v, want both true", data.UIDMirrored, data.CounterMirrored)
	}
	if got := data.UIDString(); got != "04DE5F1EACC040" {
		t.Errorf("UIDString() = %q, want %q", got, "04DE5F1EACC040")
	}
}

// AN12196 Table 3: PICCData, session key, and the file data the tag encrypted
// under this tap's own IV. The plaintext is sixteen 'x' bytes.
func TestDecryptFileDataAN12196Table3(t *testing.T) {
	data, err := DecryptPICCData(zeroKey, mustHex(t, "FDE4AFA99B5C820A2C1BB0F1C792D0EB"))
	if err != nil {
		t.Fatalf("DecryptPICCData: %v", err)
	}
	if want := mustHex(t, "04958CAA5C5E80"); !bytes.Equal(data.UID, want) {
		t.Fatalf("UID = %X, want %X", data.UID, want)
	}
	if data.ReadCounter != 1 {
		t.Fatalf("ReadCounter = %d, want 1", data.ReadCounter)
	}

	encKey, _, err := SessionKeys(zeroKey, data)
	if err != nil {
		t.Fatalf("SessionKeys: %v", err)
	}
	if want := mustHex(t, "8097D73344D53F963B09E23E03B62336"); !bytes.Equal(encKey, want) {
		t.Errorf("KSesSDMFileReadENC = %X, want %X", encKey, want)
	}

	plain, err := DecryptFileData(zeroKey, data, mustHex(t, "94592FDE69FA06E8E3B6CA686A22842B"))
	if err != nil {
		t.Fatalf("DecryptFileData: %v", err)
	}
	if want := mustHex(t, "78787878787878787878787878787878"); !bytes.Equal(plain, want) {
		t.Errorf("file data = %X, want %X", plain, want)
	}
}

// AN12196 Table 4: a tag that mirrors nothing but PICCData MACs a zero-length
// input, which is the case the CMAC padding rules are easiest to get wrong on.
func TestMACAN12196Table4(t *testing.T) {
	data, err := DecryptPICCData(zeroKey, mustHex(t, "EF963FF7828658A599F3041510671E88"))
	if err != nil {
		t.Fatalf("DecryptPICCData: %v", err)
	}

	_, macKey, err := SessionKeys(zeroKey, data)
	if err != nil {
		t.Fatalf("SessionKeys: %v", err)
	}
	if want := mustHex(t, "3FB5F6E3A807A03D5E3570ACE393776F"); !bytes.Equal(macKey, want) {
		t.Errorf("KSesSDMFileReadMAC = %X, want %X", macKey, want)
	}

	mac, err := MAC(zeroKey, data, nil)
	if err != nil {
		t.Fatalf("MAC: %v", err)
	}
	if want := mustHex(t, "94EED9EE65337086"); !bytes.Equal(mac, want) {
		t.Errorf("SDMMAC = %X, want %X", mac, want)
	}

	if err := VerifyMAC(zeroKey, data, nil, mustHex(t, "94EED9EE65337086")); err != nil {
		t.Errorf("VerifyMAC on the tag's own MAC: %v", err)
	}
}

// AN12196 Table 5: the MAC covers mirrored file data, as the ASCII of the URL
// between the encrypted data and the MAC itself. The MAC is over the text, not
// the bytes it encodes.
func TestMACOverMirroredDataAN12196Table5(t *testing.T) {
	data, err := DecryptPICCData(zeroKey, mustHex(t, "FD91EC264309878BE6345CBE53BADF40"))
	if err != nil {
		t.Fatalf("DecryptPICCData: %v", err)
	}
	if want := mustHex(t, "04958CAA5C5E80"); !bytes.Equal(data.UID, want) {
		t.Fatalf("UID = %X, want %X", data.UID, want)
	}
	if data.ReadCounter != 8 {
		t.Fatalf("ReadCounter = %d, want 8", data.ReadCounter)
	}

	input := []byte("CEE9A53E3E463EF1F459635736738962&cmac=")
	mac, err := MAC(zeroKey, data, input)
	if err != nil {
		t.Fatalf("MAC: %v", err)
	}
	if want := mustHex(t, "ECC1E7F6C6C73BF6"); !bytes.Equal(mac, want) {
		t.Errorf("SDMMAC = %X, want %X", mac, want)
	}
}

// A MAC that does not belong to the tap is refused, whether the data, the
// input or the key is wrong. Without this the vectors above only prove the
// happy path.
func TestVerifyMACRefusesWhatDoesNotMatch(t *testing.T) {
	data, err := DecryptPICCData(zeroKey, mustHex(t, "EF963FF7828658A599F3041510671E88"))
	if err != nil {
		t.Fatalf("DecryptPICCData: %v", err)
	}
	genuine := mustHex(t, "94EED9EE65337086")

	t.Run("a MAC from another tap", func(t *testing.T) {
		if err := VerifyMAC(zeroKey, data, nil, mustHex(t, "ECC1E7F6C6C73BF6")); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})

	t.Run("a replayed counter", func(t *testing.T) {
		replayed := *data
		replayed.ReadCounter++
		if err := VerifyMAC(zeroKey, &replayed, nil, genuine); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})

	t.Run("another tag's key", func(t *testing.T) {
		other := make([]byte, KeySize)
		other[0] = 1
		if err := VerifyMAC(other, data, nil, genuine); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})

	t.Run("data appended after the MAC was taken", func(t *testing.T) {
		if err := VerifyMAC(zeroKey, data, []byte("extra"), genuine); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})
}

// PICCData that does not decrypt to the documented structure is refused rather
// than read as a UID of random bytes. This is what a wrong meta read key looks
// like, and it is the only check available before the MAC.
func TestDecryptPICCDataRejectsWhatIsNotPICCData(t *testing.T) {
	t.Run("wrong key", func(t *testing.T) {
		other := make([]byte, KeySize)
		other[0] = 1
		_, err := DecryptPICCData(other, mustHex(t, "EF963FF7828658A599F3041510671E88"))
		if !errors.Is(err, ErrPICCData) {
			t.Errorf("err = %v, want ErrPICCData", err)
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		_, err := DecryptPICCData(zeroKey, mustHex(t, "EF963FF7828658A5"))
		if !errors.Is(err, ErrPICCData) {
			t.Errorf("err = %v, want ErrPICCData", err)
		}
	})

	t.Run("wrong key size", func(t *testing.T) {
		_, err := DecryptPICCData(make([]byte, 8), mustHex(t, "EF963FF7828658A599F3041510671E88"))
		if !errors.Is(err, ErrKeySize) {
			t.Errorf("err = %v, want ErrKeySize", err)
		}
	})
}

// The counter is little-endian on the wire and the session vectors carry it in
// that form, so a round trip through both is the check that neither flipped it.
func TestCounterEncoding(t *testing.T) {
	for _, ctr := range []uint32{0, 1, 8, 0x3D, 0x00FFFF, 0xFFFFFF} {
		if got := decodeCounter(encodeCounter(ctr)); got != ctr {
			t.Errorf("round trip of %d gave %d", ctr, got)
		}
	}
	if got, want := encodeCounter(0x3D), mustHex(t, "3D0000"); !bytes.Equal(got, want) {
		t.Errorf("encodeCounter(0x3D) = %X, want %X", got, want)
	}
}
