package ntag424

import (
	"bytes"
	"errors"
	"testing"
)

func TestStatusErrorMapping(t *testing.T) {
	tests := []struct {
		sw   string
		want error
	}{
		{"91AE", ErrSessionLost},
		{"911E", ErrSessionLost},
		{"917E", ErrSessionLost},
		{"91AD", ErrAuthDelay},
		{"919D", ErrPermissionDenied},
		{"91CA", nil},
		{"6A82", nil},
	}
	for _, tt := range tests {
		t.Run(tt.sw, func(t *testing.T) {
			sw := mustHex(t, tt.sw)
			_, err := CheckResponse(nil, sw, CommPlain)
			var se *StatusError
			if !errors.As(err, &se) || se.SW1 != sw[0] || se.SW2 != sw[1] {
				t.Fatalf("err = %v, want StatusError %s", err, tt.sw)
			}
			for _, sentinel := range []error{ErrSessionLost, ErrAuthDelay, ErrPermissionDenied} {
				if got, want := errors.Is(err, sentinel), sentinel == tt.want; got != want {
					t.Errorf("errors.Is(%v) = %v, want %v", sentinel, got, want)
				}
			}
		})
	}
}

func TestCheckResponseVerifiesTheMAC(t *testing.T) {
	reader, card := sessionPair(t)

	cmd, err := ChangeFileSettings(reader, NDEFFileNo, []byte{0x00, 0xEE, 0xEE})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := card.VerifyCommand(cmd, CommFull, 1); err != nil {
		t.Fatalf("VerifyCommand: %v", err)
	}
	resp, err := card.Answer(0x00, nil, CommFull)
	if err != nil {
		t.Fatal(err)
	}

	tampered := bytes.Clone(resp)
	tampered[0] ^= 0xFF
	if _, err := CheckResponse(reader, tampered, CommFull); !errors.Is(err, ErrMACMismatch) {
		t.Errorf("tampered MAC error = %v, want ErrMACMismatch", err)
	}
	if _, err := CheckResponse(reader, resp, CommFull); err != nil {
		t.Errorf("genuine response: %v", err)
	}
	if reader.Counter() != card.Counter() {
		t.Errorf("counters diverged: %d and %d", reader.Counter(), card.Counter())
	}
}

func TestCheckChangeKeyResponse(t *testing.T) {
	if err := CheckChangeKeyResponse(nil, mustHex(t, "9100"), 0, 0); err != nil {
		t.Errorf("own key, bare status: %v", err)
	}
	if err := CheckChangeKeyResponse(nil, mustHex(t, "91AE"), 0, 0); !errors.Is(err, ErrSessionLost) {
		t.Errorf("own key, 91AE: %v", err)
	}

	reader, card := sessionPair(t)
	cmd, err := reader.Command(insChangeKey, []byte{0x02}, make([]byte, 21), CommFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := card.VerifyCommand(cmd, CommFull, 1); err != nil {
		t.Fatal(err)
	}
	resp, _ := card.Answer(0x00, nil, CommFull)
	if err := CheckChangeKeyResponse(reader, resp, 0x02, 0x00); err != nil {
		t.Errorf("other key: %v", err)
	}
}

func TestIsLRPAuthResponse(t *testing.T) {
	rndB := bytes.Repeat([]byte{0xAA}, 16)
	aes := append(bytes.Clone(rndB), 0x91, 0xAF)
	lrp := append(append([]byte{0x01}, rndB...), 0x91, 0xAF)

	if IsLRPAuthResponse(aes) {
		t.Error("AES-mode reply reported as LRP")
	}
	if !IsLRPAuthResponse(lrp) {
		t.Error("LRP reply not detected")
	}
	for name, resp := range map[string][]byte{
		"empty":         nil,
		"error status":  {0x91, 0xAD},
		"wrong mode":    append(append([]byte{0x00}, rndB...), 0x91, 0xAF),
		"not AF":        append(append([]byte{0x01}, rndB...), 0x91, 0x00),
		"short LRP":     lrp[1:],
		"one byte over": append(bytes.Clone(lrp), 0x00),
	} {
		if IsLRPAuthResponse(resp) {
			t.Errorf("%s reported as LRP", name)
		}
	}
}
