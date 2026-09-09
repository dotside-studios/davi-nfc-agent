package ntag424

import (
	"bytes"
	"crypto/aes"
	"encoding/hex"
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

// RFC 4493's test vectors for AES-128 CMAC, which cover the three shapes the
// algorithm branches on: an empty message, whole blocks, and a final partial
// block that has to be padded.
func TestCMACAgainstRFC4493(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}

	t.Run("subkeys", func(t *testing.T) {
		k1, k2 := cmacSubkeys(block)
		if want := mustHex(t, "fbeed618357133667c85e08f7236a8de"); !bytes.Equal(k1, want) {
			t.Errorf("K1 = %X, want %X", k1, want)
		}
		if want := mustHex(t, "f7ddac306ae266ccf90bc11ee46d513b"); !bytes.Equal(k2, want) {
			t.Errorf("K2 = %X, want %X", k2, want)
		}
	})

	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "empty message",
			msg:  "",
			want: "bb1d6929e95937287fa37d129b756746",
		},
		{
			name: "one whole block",
			msg:  "6bc1bee22e409f96e93d7e117393172a",
			want: "070a16b46b4d4144f79bdd9dd04a287c",
		},
		{
			name: "two blocks and a partial one",
			msg: "6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411",
			want: "dfa66747de9ae63030ca32611497c827",
		},
		{
			name: "four whole blocks",
			msg: "6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411e5fbc1191a0a52ef" +
				"f69f2445df4f9b17ad2b417be66c3710",
			want: "51f0bebf7e3b9d92fc49741779363cfe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cmac(block, mustHex(t, tt.msg))
			if want := mustHex(t, tt.want); !bytes.Equal(got, want) {
				t.Errorf("cmac = %X, want %X", got, want)
			}
		})
	}
}

// The message must not be modified in place: SDM MACs a slice of a URL the
// caller still holds, and padding the final block would write into it.
func TestCMACDoesNotModifyItsInput(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 16))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}

	msg := []byte("a partial final block")
	before := append([]byte(nil), msg...)
	cmac(block, msg)
	if !bytes.Equal(msg, before) {
		t.Errorf("message changed: %q, want %q", msg, before)
	}
}
