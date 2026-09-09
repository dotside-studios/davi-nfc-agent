package ntag424

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// tableSession is the session AN12196 Table 14's authentication leaves behind,
// which Tables 7 and 17 then send commands in.
func tableSession(t *testing.T, ti, encKey, macKey string) *Session {
	t.Helper()
	s, err := newSession(mustHex(t, ti), mustHex(t, encKey), mustHex(t, macKey))
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	return s
}

// AN12196 Table 7: GetFileSettings in CommMode.MAC, command and response. The
// MAC binds the instruction, the counter and the transaction, so reproducing
// the published APDU proves all three go in in the right order and encoding.
func TestSessionCommModeMACAN12196Table7(t *testing.T) {
	s := tableSession(t,
		"7A21085E",
		"00000000000000000000000000000000", // not used by this command
		"8248134A386E86EB7FAF54A52E536CB6",
	)

	cmd, err := s.Command(0xF5, mustHex(t, "02"), nil, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if want := mustHex(t, "90F5000009026597A457C8CD442C00"); !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}

	// The card answers with its settings and a MAC over them, carrying the
	// counter plus one.
	settings := "0040EEEE000100D1FE001F00004400004400002000006A0000"
	response := mustHex(t, settings+"2A474282E7A47986"+"9100")

	data, err := s.Response(response, CommMAC)
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if want := mustHex(t, settings); !bytes.Equal(data, want) {
		t.Errorf("response data = %X, want %X", data, want)
	}
	if s.Counter() != 1 {
		t.Errorf("counter = %d, want 1 after one exchange", s.Counter())
	}
}

// AN12196 Table 17: WriteData in CommMode.Full. The command's IV is derived
// from its place in the session, which is the value published at step 9 and the
// one thing that cannot be got right by accident.
func TestSessionCommModeFullAN12196Table17(t *testing.T) {
	s := tableSession(t,
		"9D00C4DF",
		"1309C877509E5A215007FF0ED19CA564",
		"4C6626F5E72EA694202139295C7A7FC7",
	)

	if got, want := s.iv(0xA5, 0x5A, 0), mustHex(t, "D2CB7277A17841A06654A48188C1F8F5"); !bytes.Equal(got, want) {
		t.Fatalf("IVc = %X, want %X", got, want)
	}

	// The published ciphertext decrypts to the NDEF message the note writes,
	// which is what shows the mode and the IV are both right: the plaintext is
	// readable and is the URL from the document.
	ciphertext := mustHex(t, "421C73A27D827658AF481FDFF20A5025B559D0E3AA21E58D347F343CFFC768BF"+
		"E596C706BC00F2176781D4B0242642A0FF5A42C461AAF894D9A1284B8C76BCFA"+
		"658ACD40555D362E08DB15CF421B51283F9064BCBE20E96CAE545B407C9D651A"+
		"3315B27373772E5DA2367D2064AE054AF996C6F1F669170FA88CE8C4E3A4A7BB"+
		"BEF0FD971FF532C3A802AF745660F2B4")

	plain, err := s.decryptWithIV(s.iv(0xA5, 0x5A, 0), ciphertext)
	if err != nil {
		t.Fatalf("decrypting the published command data: %v", err)
	}
	if want := "choose.url.com/ntag424?e="; !strings.Contains(string(plain), want) {
		t.Fatalf("decrypted command data does not contain %q; got %q", want, plain)
	}

	// Encrypting that plaintext again reproduces the published ciphertext, so
	// the padding and the chaining match as well as the IV.
	again, err := s.encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(again, ciphertext) {
		t.Errorf("re-encrypted command data = %X, want %X", again, ciphertext)
	}

	// The card's answer carries no data, only a MAC over the status, the
	// counter plus one and the transaction.
	if _, err := s.Response(mustHex(t, "FC222E5F7A5424529100"), CommFull); err != nil {
		t.Fatalf("Response: %v", err)
	}
	if s.Counter() != 1 {
		t.Errorf("counter = %d, want 1", s.Counter())
	}
}

// A response whose MAC does not match is refused, and the session does not
// advance: the two sides stay in step so the next command is still the one the
// card expects.
func TestSessionRefusesAnUnverifiedResponse(t *testing.T) {
	s := tableSession(t, "7A21085E", zeroHex, "8248134A386E86EB7FAF54A52E536CB6")

	tests := map[string]string{
		"a flipped MAC bit":      "0040EEEE000100D1FE001F00004400004400002000006A00002A474282E7A47987" + "9100",
		"altered response data":  "0040EEEE000100D1FE001F00004400004400002000006A0001" + "2A474282E7A47986" + "9100",
		"a MAC and nothing else": "2A474282E7A479869100",
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			before := s.Counter()
			if _, err := s.Response(mustHex(t, response), CommMAC); !errors.Is(err, ErrMACMismatch) {
				t.Errorf("err = %v, want ErrMACMismatch", err)
			}
			if s.Counter() != before {
				t.Errorf("counter advanced to %d on a refused response", s.Counter())
			}
		})
	}
}

// An error status word from the card is reported, not read as data.
func TestSessionReportsAnErrorStatus(t *testing.T) {
	s := tableSession(t, "7A21085E", zeroHex, "8248134A386E86EB7FAF54A52E536CB6")
	if _, err := s.Response(mustHex(t, "911C"), CommMAC); err == nil {
		t.Error("an error status word was accepted")
	}
	if s.Counter() != 0 {
		t.Errorf("counter = %d, want 0 after a refused command", s.Counter())
	}
}

// The counter goes into every MAC, so the same command sent twice in a session
// is two different APDUs. That is what stops one being captured and replayed.
func TestSessionCounterMakesEachCommandDistinct(t *testing.T) {
	s := tableSession(t, "7A21085E", zeroHex, "8248134A386E86EB7FAF54A52E536CB6")

	first, err := s.Command(0xF5, mustHex(t, "02"), nil, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	response := mustHex(t, "0040EEEE000100D1FE001F00004400004400002000006A00002A474282E7A479869100")
	if _, err := s.Response(response, CommMAC); err != nil {
		t.Fatalf("Response: %v", err)
	}

	second, err := s.Command(0xF5, mustHex(t, "02"), nil, CommMAC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("the same command twice produced the same APDU; the counter is not in the MAC")
	}

	// And the card's earlier answer no longer verifies against the new counter.
	if _, err := s.Response(response, CommMAC); !errors.Is(err, ErrMACMismatch) {
		t.Errorf("a replayed response was accepted: %v", err)
	}
}

// Plain mode still counts the command, because the card counts it too.
func TestSessionPlainModeStillCounts(t *testing.T) {
	s := tableSession(t, "7A21085E", zeroHex, "8248134A386E86EB7FAF54A52E536CB6")

	cmd, err := s.Command(0xF5, mustHex(t, "02"), nil, CommPlain)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if want := mustHex(t, "90F50000010200"); !bytes.Equal(cmd, want) {
		t.Errorf("C-APDU = %X, want %X", cmd, want)
	}
	if _, err := s.Response(mustHex(t, "00009100"), CommPlain); err != nil {
		t.Fatalf("Response: %v", err)
	}
	if s.Counter() != 1 {
		t.Errorf("counter = %d, want 1", s.Counter())
	}
}

// Padding is removable in every case, including data that already fills a
// block: without the extra block, the last byte of real data would be eaten.
func TestPaddingRoundTrips(t *testing.T) {
	for _, size := range []int{0, 1, 15, 16, 17, 31, 32} {
		data := bytes.Repeat([]byte{0xAB}, size)
		back, err := unpadISO9797(padISO9797(data))
		if err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}
		if !bytes.Equal(back, data) {
			t.Errorf("%d bytes round-tripped to %X", size, back)
		}
	}

	if _, err := unpadISO9797(bytes.Repeat([]byte{0x00}, 16)); err == nil {
		t.Error("a block with no padding marker was accepted")
	}
}

const zeroHex = "00000000000000000000000000000000"
