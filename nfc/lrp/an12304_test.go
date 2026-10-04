package lrp

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// testdata/an12304.json holds every vector in sections 3.2 to 3.4 of NXP's
// AN12304 Rev. 1.1, extracted from the document's text.
type an12304Vectors struct {
	Eval []struct {
		KEY, IV, FINALIZE, UPDATEDKEY, RES string
	} `json:"eval"`
	LRICB []struct {
		KEY, IV, USEPADDING, PT, CT string
	} `json:"lricb"`
	CMAC []struct {
		KEY, Kx, MSG, MAC string
	} `json:"cmac"`
}

func loadAN12304(t *testing.T) an12304Vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/an12304.json")
	if err != nil {
		t.Fatal(err)
	}
	var v an12304Vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Eval) == 0 || len(v.LRICB) == 0 || len(v.CMAC) == 0 {
		t.Fatal("no vectors loaded")
	}
	return v
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// The IVs here may have an odd number of nibbles, so they are walked as
// nibbles rather than bytes.
func TestEvalAN12304Section3_2(t *testing.T) {
	for i, v := range loadAN12304(t).Eval {
		update := int(v.UPDATEDKEY[0] - '0')
		k, err := New(decodeHex(t, v.KEY), update)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		nibbles := make([]byte, len(v.IV))
		for j, c := range v.IV {
			n, err := hex.DecodeString("0" + string(c))
			if err != nil {
				t.Fatalf("vector %d: bad IV %q", i, v.IV)
			}
			nibbles[j] = n[0]
		}
		got := k.evalNibbles(nibbles, v.FINALIZE == "1")
		if want := decodeHex(t, v.RES); !bytes.Equal(got, want) {
			t.Errorf("vector %d (IV %s): got %X, want %X", i, v.IV, got, want)
		}
	}
}

// With USEPADDING the plaintext is padded with 0x80 and zeros to whole blocks,
// always adding at least one byte, before it is enciphered under k0.
func TestLRICBAN12304Section3_3(t *testing.T) {
	for i, v := range loadAN12304(t).LRICB {
		k, err := New(decodeHex(t, v.KEY), UpdateMAC)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		pt := decodeHex(t, v.PT)
		if v.USEPADDING == "1" {
			pt = append(pt, 0x80)
			for len(pt)%BlockSize != 0 {
				pt = append(pt, 0)
			}
		}
		ct := decodeHex(t, v.CT)
		got, err := k.Encrypt(decodeHex(t, v.IV), pt)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if !bytes.Equal(got, ct) {
			t.Errorf("vector %d: encrypt got %X, want %X", i, got, ct)
		}
		back, err := k.Decrypt(decodeHex(t, v.IV), ct)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if !bytes.Equal(back, pt) {
			t.Errorf("vector %d: decrypt got %X, want %X", i, back, pt)
		}
	}
}

func TestCMACAN12304Section3_4(t *testing.T) {
	for i, v := range loadAN12304(t).CMAC {
		k, err := New(decodeHex(t, v.KEY), UpdateMAC)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if got, want := k.CMAC(decodeHex(t, v.MSG)), decodeHex(t, v.MAC); !bytes.Equal(got, want) {
			t.Errorf("vector %d (MSG %s): got %X, want %X", i, v.MSG, got, want)
		}
	}
}
