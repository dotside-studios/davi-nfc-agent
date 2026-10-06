package ntag424

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// AN12321 Rev. 1.0 Table 3: SetConfiguration switching the card to LRP, sent in
// an AES session. The data field is step 25's; step 27 repeats it with a stray
// 00 after the option byte, which its own Lc of 19h rules out.
func TestEnableLRPAN12321Table3(t *testing.T) {
	s, err := ev2.NewSessionAt(mustHex(t, "ED56F6E6"),
		mustHex(t, "66A8CB93269DC9BC2885B7A91B9C697B"),
		mustHex(t, "7DE5F7E244A46D22E536804D07E8D70E"), 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := EnableLRP(s)
	if err != nil {
		t.Fatal(err)
	}
	want := mustHex(t, "905C000019"+"0541B2BA963075730426D0858D2AA6C498"+"2F579E77FAB49F83"+"00")
	if !bytes.Equal(cmd, want) {
		t.Errorf("EnableLRP = %X, want %X", cmd, want)
	}
}
