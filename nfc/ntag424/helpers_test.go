package ntag424

import (
	"encoding/hex"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// zeroHex is an all-zero AES key, for a table whose command does not use one.
const zeroHex = "00000000000000000000000000000000"

// tableSession builds the session an application-note table starts from, at the
// counter that table's transaction had reached, so a published APDU can be
// reproduced without running an authentication first.
func tableSession(t *testing.T, ti, encKey, macKey string, counter uint16) *Session {
	t.Helper()
	s, err := ev2.NewSessionAt(mustHex(t, ti), mustHex(t, encKey), mustHex(t, macKey), counter)
	if err != nil {
		t.Fatalf("NewSessionAt: %v", err)
	}
	return s
}
