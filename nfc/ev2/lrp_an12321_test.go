package ev2

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/lrp"
)

// AN12321 Rev. 1.0, Table 2: AuthenticateLRPFirst with the all-zero key. The
// table names key 3 but its command carries key number 0, which is what is
// checked here.
func TestAuthenticateLRPFirstAN12321Table2(t *testing.T) {
	key := make([]byte, KeySize)
	rndA := mustHex(t, "74D7DF6A2CEC0B72B412DE0D2B1117E6")
	rndB := mustHex(t, "56109A31977C855319CD4618C9D2AED2")

	auth, err := NewLRPAuthenticator(AuthFirst, 0x00, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth.SetRandom(&fixedRandom{rndA})

	if got, want := auth.Command(), mustHex(t, "9071000008000602000000000000"); !bytes.Equal(got, want) {
		t.Errorf("first command = %X, want %X", got, want)
	}

	second, err := auth.Challenge(mustHex(t, "0156109A31977C855319CD4618C9D2AED291AF"))
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	want := mustHex(t, "90AF00002074D7DF6A2CEC0B72B412DE0D2B1117E6189B59DCEDC31A3D3F38EF8D4810B3B400")
	if !bytes.Equal(second, want) {
		t.Errorf("second command = %X, want %X", second, want)
	}

	master, err := DeriveLRPSessionKey(key, rndA, rndB)
	if err != nil {
		t.Fatal(err)
	}
	updated := lrp.UpdatedKeys(master)
	if want := mustHex(t, "F56CADE598CC2A3FE47E438CFEB885DB"); !bytes.Equal(updated[lrp.UpdateMAC], want) {
		t.Errorf("SesAuthMACUpdateKey = %X, want %X", updated[lrp.UpdateMAC], want)
	}
	if want := mustHex(t, "E9043D65AB21C0C422781099AB25EFDD"); !bytes.Equal(updated[lrp.UpdateENC], want) {
		t.Errorf("SesAuthENCUpdateKey = %X, want %X", updated[lrp.UpdateENC], want)
	}

	session, err := auth.Finish(mustHex(t, "F4FC209D9D60623588B299FA5D6B2D710125F8547D9FB8D572C90D2C2A14E2359100"))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got, want := session.TI(), mustHex(t, "58EE9424"); !bytes.Equal(got, want) {
		t.Errorf("TI = %X, want %X", got, want)
	}
}
