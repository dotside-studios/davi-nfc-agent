package ntag424

import (
	"bytes"
	"crypto/aes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
	"github.com/dotside-studios/davi-nfc-agent/nfc/lrp"
)

// An LRP tap, built here from the primitives by hand and verified through the
// package's own entry points. These pin the package to its own derivation, not
// to a published LRP SUN message: no AN12196 LRP example is pinned, so a
// mistake in how the session vector or MAC truncation is read from the
// specification would pass these.

var (
	lrpMetaKey = mustHexLRP("00112233445566778899AABBCCDDEEFF")
	lrpFileKey = mustHexLRP("FFEEDDCCBBAA99887766554433221100")
	lrpUID     = mustHexLRP("04958CAA5C5E80")
)

func mustHexLRP(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// lrpTap builds what an LRP tag mirrors for a UID and counter: the PICCRand and
// encrypted PICCData, and the MAC over input.
func lrpTap(t *testing.T, ctr uint32, input []byte) (picc, mac []byte) {
	t.Helper()
	rand := mustHexLRP("0123456789ABCDEF")
	plain := make([]byte, 16)
	plain[0] = 0xC7
	copy(plain[1:], lrpUID)
	copy(plain[8:], []byte{byte(ctr), byte(ctr >> 8), byte(ctr >> 16)})

	metaKey, err := lrp.New(lrpMetaKey, lrp.UpdateMAC)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := metaKey.Encrypt(rand, plain)
	if err != nil {
		t.Fatal(err)
	}
	picc = append(append([]byte(nil), rand...), enc...)

	sv := append([]byte{0x00, 0x01, 0x00, 0x80}, lrpUID...)
	sv = append(sv, byte(ctr), byte(ctr>>8), byte(ctr>>16), 0x1E, 0xE1)
	block, err := aes.NewCipher(lrpFileKey)
	if err != nil {
		t.Fatal(err)
	}
	master := ev2.CMAC(block, sv)
	macKey, err := lrp.New(master, lrp.UpdateMAC)
	if err != nil {
		t.Fatal(err)
	}
	return picc, ev2.TruncateMAC(macKey.CMAC(input))
}

func lrpKeys() Keys { return Keys{MetaRead: lrpMetaKey, FileRead: lrpFileKey, LRP: true} }

func lrpURL(picc, mac []byte) string {
	return "https://example.test/t?e=" + strings.ToUpper(hex.EncodeToString(picc)) +
		"&c=" + strings.ToUpper(hex.EncodeToString(mac))
}

func TestVerifyURLLRP(t *testing.T) {
	picc, mac := lrpTap(t, 0x0102, nil)
	tap, err := VerifyURL(lrpURL(picc, mac), lrpKeys())
	if err != nil {
		t.Fatalf("VerifyURL: %v", err)
	}
	if !bytes.Equal(tap.UID, lrpUID) || tap.ReadCounter != 0x0102 {
		t.Errorf("tap = %X / %d, want %X / %d", tap.UID, tap.ReadCounter, lrpUID, 0x0102)
	}
}

func TestVerifyURLLRPRejects(t *testing.T) {
	picc, mac := lrpTap(t, 7, nil)

	t.Run("flipped MAC", func(t *testing.T) {
		bad := bytes.Clone(mac)
		bad[0] ^= 1
		if _, err := VerifyURL(lrpURL(picc, bad), lrpKeys()); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})
	t.Run("altered counter ciphertext", func(t *testing.T) {
		bad := bytes.Clone(picc)
		bad[len(bad)-1] ^= 1
		if _, err := VerifyURL(lrpURL(bad, mac), lrpKeys()); err == nil {
			t.Error("a tampered PICCData verified")
		}
	})
	t.Run("wrong file key", func(t *testing.T) {
		keys := lrpKeys()
		keys.FileRead = lrpMetaKey
		if _, err := VerifyURL(lrpURL(picc, mac), keys); !errors.Is(err, ErrMACMismatch) {
			t.Errorf("err = %v, want ErrMACMismatch", err)
		}
	})
	t.Run("AES keys for an LRP tap", func(t *testing.T) {
		keys := lrpKeys()
		keys.LRP = false
		if _, err := VerifyURL(lrpURL(picc, mac), keys); !errors.Is(err, ErrPICCData) {
			t.Errorf("err = %v, want ErrPICCData", err)
		}
	})
	t.Run("LRP keys for an AES tap", func(t *testing.T) {
		url := "https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337086"
		if _, err := VerifyURL(url, Keys{MetaRead: zeroKey, FileRead: zeroKey, LRP: true}); !errors.Is(err, ErrPICCData) {
			t.Errorf("err = %v, want ErrPICCData", err)
		}
	})
	t.Run("encrypted file data", func(t *testing.T) {
		data := &PICCData{UID: lrpUID, UIDMirrored: true, CounterMirrored: true, LRP: true}
		if _, err := DecryptFileData(lrpFileKey, data, make([]byte, 16)); !errors.Is(err, ErrLRPFileData) {
			t.Errorf("err = %v, want ErrLRPFileData", err)
		}
	})
}

func TestVerifyURLFreshLRP(t *testing.T) {
	store := &MemoryCounterStore{}
	picc, mac := lrpTap(t, 3, nil)
	url := lrpURL(picc, mac)
	if _, err := VerifyURLFresh(url, lrpKeys(), store); err != nil {
		t.Fatalf("first tap: %v", err)
	}
	if _, err := VerifyURLFresh(url, lrpKeys(), store); !errors.Is(err, ErrReplay) {
		t.Errorf("replay = %v, want ErrReplay", err)
	}
	picc, mac = lrpTap(t, 4, nil)
	if _, err := VerifyURLFresh(lrpURL(picc, mac), lrpKeys(), store); err != nil {
		t.Errorf("next tap: %v", err)
	}
}

func TestVerifyURLLRPPlainMirrors(t *testing.T) {
	data := &PICCData{UID: lrpUID, ReadCounter: 9, UIDMirrored: true, CounterMirrored: true, LRP: true}
	mac, err := MAC(lrpFileKey, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.test/t?uid=" + strings.ToUpper(hex.EncodeToString(lrpUID)) +
		"&ctr=000009&c=" + strings.ToUpper(hex.EncodeToString(mac))
	tap, err := VerifyURL(url, lrpKeys())
	if err != nil {
		t.Fatalf("VerifyURL: %v", err)
	}
	if tap.ReadCounter != 9 {
		t.Errorf("counter = %d, want 9", tap.ReadCounter)
	}
	if _, err := VerifyURL(url, Keys{FileRead: lrpFileKey}); !errors.Is(err, ErrMACMismatch) {
		t.Errorf("AES keys for a plain LRP tap = %v, want ErrMACMismatch", err)
	}
}

func TestPlanSDMLRP(t *testing.T) {
	opts := SDMOptions{Read: AccessFree, Write: AccessNever, ReadWrite: AccessNever, LRP: true}
	plan, err := PlanSDM("https://example.test/t?e={picc}&c={mac}", opts)
	if err != nil {
		t.Fatal(err)
	}
	aesPlan, err := PlanSDM("https://example.test/t?e={picc}&c={mac}", SDMOptions{Read: AccessFree})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Settings.MACOffset-aesPlan.Settings.MACOffset, uint32(lrpPiccMirrorChars-piccMirrorChars); got != want {
		t.Errorf("LRP mirror is %d characters wider, want %d", got, want)
	}
	if _, err := PlanSDM("https://example.test/t?e={picc}&d={enc}&c={mac}", opts); err == nil {
		t.Error("{enc} was planned for an LRP tag")
	}
}

func TestParseURLPICCDataSizes(t *testing.T) {
	_, err := ParseURL("https://example.test/t?e=0011223344&c=94EED9EE65337086")
	if !errors.Is(err, ErrPICCData) {
		t.Errorf("err = %v, want ErrPICCData", err)
	}
}
