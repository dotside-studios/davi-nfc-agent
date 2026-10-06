package nfctest

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// The driver against an emulated card in LRP mode. The emulator and the driver
// share the ev2 and lrp packages, so these prove the driver picks the suite from
// the card's reply and drives it end to end, not that the exchange matches real
// hardware. The pieces NXP publishes examples for are pinned in nfc/lrp and
// nfc/ev2.

func n4LRPKeys() nfc.NTAG424Keys {
	keys := n4Keys()
	keys.AllowLRP = true
	return keys
}

func n4LRPCard(t *testing.T, fs ntag424.FileSettings, opts ...NTAG424Option) *EmulatedCard {
	t.Helper()
	opts = append([]NTAG424Option{
		NTAG424WithLRP(),
		NTAG424WithKeys(map[byte][]byte{0: n4Key0, 1: n4Key1}),
		NTAG424WithFileSettings(ntag424.NDEFFileNo, fs),
	}, opts...)
	return NTAG424(ntag424UID, opts...).WithNTAG424Keys(n4LRPKeys())
}

func TestNTAG424LRPAuthenticatesAndReportsTheSuite(t *testing.T) {
	card := n4LRPCard(t, n4Protected(ntag424.CommMAC))
	op := card.Tag().(nfc.NTAG424Operator)

	if v, err := op.GetKeyVersion(1); err != nil || v != 0 {
		t.Fatalf("GetKeyVersion = %d, %v", v, err)
	}
	if _, err := op.ReadSig(); err != nil {
		t.Fatalf("ReadSig: %v", err)
	}
	if !card.NTAG424Authenticated() {
		t.Error("no session on the card after an LRP authentication")
	}
	if !card.Tag().Capabilities().LRP {
		t.Error("capabilities do not report the card as LRP")
	}
	if err := nfc.AssertCapabilitiesConsistent(card.Tag()); err != nil {
		t.Error(err)
	}
}

func TestNTAG424LRPProtectedNDEFReadWrite(t *testing.T) {
	for _, mode := range []ntag424.CommMode{ntag424.CommMAC, ntag424.CommFull} {
		card := n4LRPCard(t, n4Protected(mode))
		tag := card.Tag()

		want := n4Message(t, "https://davi.social/t/lrp/protected")
		if err := tag.WriteData(want); err != nil {
			t.Fatalf("mode %d: WriteData: %v", mode, err)
		}
		got, err := tag.ReadData()
		if err != nil {
			t.Fatalf("mode %d: ReadData: %v", mode, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("mode %d: read % X, want % X", mode, got, want)
		}
		if raw := card.NTAG424FileData(2); int(raw[0])<<8|int(raw[1]) != len(want) {
			t.Errorf("mode %d: NLEN in the card is % X, want %d", mode, raw[:2], len(want))
		}
		if counter, ok := card.NTAG424SessionCounter(); !ok || counter < 4 {
			t.Errorf("mode %d: session counter = %d, %v", mode, counter, ok)
		}
	}
}

func TestNTAG424LRPLargeFullWriteSpansBlocksAndChunks(t *testing.T) {
	card := n4LRPCard(t, n4Protected(ntag424.CommFull))
	tag := card.Tag()

	want := n4Message(t, "https://davi.social/t/"+string(bytes.Repeat([]byte("x"), 200)))
	if err := tag.WriteData(want); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("a multi-chunk LRP read did not return what was written")
	}
}

func TestNTAG424LRPSessionSurvivesAcrossOperations(t *testing.T) {
	card := n4LRPCard(t, n4Protected(ntag424.CommMAC))
	tag := card.Tag()
	if err := tag.WriteData(n4Message(t, "https://davi.social/t/a")); err != nil {
		t.Fatal(err)
	}
	counter, _ := card.NTAG424SessionCounter()
	if _, err := tag.ReadData(); err != nil {
		t.Fatal(err)
	}
	if after, _ := card.NTAG424SessionCounter(); after <= counter {
		t.Error("the read did not reuse the open LRP session")
	}
}

func TestNTAG424LRPIsRefusedUnlessAllowed(t *testing.T) {
	card := NTAG424(ntag424UID,
		NTAG424WithLRP(),
		NTAG424WithKeys(map[byte][]byte{0: n4Key0, 1: n4Key1}),
		NTAG424WithFileSettings(2, n4Protected(ntag424.CommMAC)),
	).WithNTAG424Keys(n4Keys())

	_, err := card.Tag().(nfc.NTAG424Operator).ReadSig()
	if !errors.Is(err, ntag424.ErrLRP) || !nfc.IsAuthError(err) {
		t.Fatalf("ReadSig = %v, want an auth error wrapping ErrLRP", err)
	}
	if !card.Tag().Capabilities().LRP {
		t.Error("an LRP card refused for lack of the opt-in is not reported as LRP")
	}
	if card.NTAG424Authenticated() {
		t.Error("the card holds a session the driver should not have opened")
	}
}

func TestNTAG424LRPWrongKeyIsTriedOnce(t *testing.T) {
	card := NTAG424(ntag424UID,
		NTAG424WithLRP(),
		NTAG424WithKeys(map[byte][]byte{0: bytes.Repeat([]byte{0x77}, 16)}),
	).WithNTAG424Keys(nfc.NTAG424Keys{Slots: map[byte][]byte{0: n4Key0}, AllowLRP: true})
	op := card.Tag().(nfc.NTAG424Operator)

	if _, err := op.ReadSig(); err == nil {
		t.Fatal("ReadSig with a wrong key succeeded")
	}
	failed := card.NTAG424FailedAuths()
	if failed == 0 {
		t.Error("the card counted no failed authentication")
	}
	if _, err := op.ReadSig(); err == nil {
		t.Fatal("second ReadSig succeeded")
	}
	if card.NTAG424FailedAuths() != failed {
		t.Error("a refused key was tried again")
	}
}

func TestNTAG424LRPConfigureSDMEndToEnd(t *testing.T) {
	zero := make([]byte, 16)
	card := NTAG424(ntag424UID, NTAG424WithLRP()).
		WithNTAG424Keys(nfc.NTAG424Keys{Master: zero, AllowLRP: true})
	op := card.Tag().(nfc.NTAG424Operator)

	plan, err := ntag424.PlanSDM("https://davi.social/t?p={picc}&m={mac}", ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: 0, Change: 0,
		Read: ntag424.AccessFree, Write: 0, ReadWrite: ntag424.AccessNever,
		LRP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := op.ConfigureSDM(plan)
	if err != nil {
		t.Fatalf("ConfigureSDM: %v", err)
	}
	if res.Tap == nil || res.Tap.UIDString() != ntag424UID {
		t.Fatalf("result = %+v, want a verified tap of the card", res)
	}

	names := ntag424.Names{PICCData: []string{"p"}, MAC: []string{"m"}}
	keys := ntag424.Keys{MetaRead: zero, FileRead: zero, LRP: true}
	last := res.Tap.ReadCounter
	for i := 0; i < 3; i++ {
		data, err := card.Tag().ReadData()
		if err != nil {
			t.Fatal(err)
		}
		url := n4URI(t, data)
		tap, err := ntag424.VerifyURLWith(url, keys, names)
		if err != nil {
			t.Fatalf("tap %d: %v", i, err)
		}
		if tap.ReadCounter <= last {
			t.Errorf("counter %d did not advance past %d", tap.ReadCounter, last)
		}
		last = tap.ReadCounter
		if _, err := ntag424.VerifyURLWith(url, ntag424.Keys{MetaRead: zero, FileRead: zero}, names); err == nil {
			t.Error("an LRP URL verified under AES keys")
		}
	}
}

// Switching a card to LRP: it answers under the AES session that sent the
// switch, then refuses AES, drops its SDM configuration, and is driven only
// with AllowLRP.
func TestNTAG424EnableLRP(t *testing.T) {
	plan, err := ntag424.PlanSDM("https://davi.social/t?p={picc}&m={mac}", ntag424.SDMOptions{
		MetaRead: 1, FileRead: 1, CounterRet: 1, Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	card := NTAG424(ntag424UID,
		NTAG424WithKeys(map[byte][]byte{0: n4Key0, 1: n4Key1}),
		NTAG424WithSDM(plan),
	).WithNTAG424Keys(n4Keys())
	if card.Tag().Capabilities().LRP {
		t.Fatal("a factory card reports LRP")
	}

	if err := card.Tag().(nfc.NTAG424LRPSwitch).EnableLRP(); err != nil {
		t.Fatalf("EnableLRP: %v", err)
	}
	if card.NTAG424FileSettings(ntag424.NDEFFileNo).SDMEnabled {
		t.Error("SDM is still enabled after the switch")
	}

	op := card.Tag().(nfc.NTAG424Operator)
	if _, err := op.ReadSig(); !errors.Is(err, ntag424.ErrLRP) {
		t.Errorf("ReadSig without AllowLRP: %v, want ErrLRP", err)
	}

	card.WithNTAG424Keys(n4LRPKeys())
	if _, err := op.ReadSig(); err != nil {
		t.Fatalf("ReadSig with AllowLRP: %v", err)
	}
	if !card.Tag().Capabilities().LRP {
		t.Error("capabilities do not report the switched card as LRP")
	}
}
