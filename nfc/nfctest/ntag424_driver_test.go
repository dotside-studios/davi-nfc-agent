package nfctest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

var (
	n4Key0 = bytes.Repeat([]byte{0x10}, 16)
	n4Key1 = bytes.Repeat([]byte{0x11}, 16)
)

func n4Keys() nfc.NTAG424Keys {
	return nfc.NTAG424Keys{Slots: map[byte][]byte{0: n4Key0, 1: n4Key1}}
}

func n4Provisioned(t *testing.T, fs ntag424.FileSettings, opts ...NTAG424Option) *EmulatedCard {
	t.Helper()
	opts = append([]NTAG424Option{
		NTAG424WithKeys(map[byte][]byte{0: n4Key0, 1: n4Key1}),
		NTAG424WithFileSettings(ntag424.NDEFFileNo, fs),
	}, opts...)
	return NTAG424(ntag424UID, opts...).WithNTAG424Keys(n4Keys())
}

func n4Message(t *testing.T, uri string) []byte {
	t.Helper()
	msg, err := (&nfc.NDEFMessageBuilder{Records: []nfc.NDEFRecordBuilder{&nfc.NDEFURI{Content: uri}}}).Build()
	if err != nil {
		t.Fatal(err)
	}
	data, err := msg.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func n4URI(t *testing.T, data []byte) string {
	t.Helper()
	msg, err := nfc.DecodeNDEF(data)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := msg.GetURI()
	if err != nil {
		t.Fatal(err)
	}
	return uri
}

func n4Protected(mode ntag424.CommMode) ntag424.FileSettings {
	return ntag424.FileSettings{
		CommMode: mode, ReadWrite: ntag424.AccessNever, Change: 0, Read: 1, Write: 1,
	}
}

func TestNTAG424ProtectedNDEFReadWrite(t *testing.T) {
	for _, mode := range []ntag424.CommMode{ntag424.CommMAC, ntag424.CommFull} {
		card := n4Provisioned(t, n4Protected(mode))
		tag := card.Tag()

		want := n4Message(t, "https://davi.social/t/protected")
		if err := tag.WriteData(want); err != nil {
			t.Fatalf("mode %d: WriteData: %v", mode, err)
		}
		if !card.NTAG424Authenticated() {
			t.Errorf("mode %d: no session after a protected write", mode)
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
	}
}

func TestNTAG424ProtectedFileWithoutKeyHasNoPayload(t *testing.T) {
	card := NTAG424(ntag424UID,
		NTAG424WithKeys(map[byte][]byte{1: n4Key1}),
		NTAG424WithFileSettings(2, n4Protected(ntag424.CommMAC)))
	tag := card.Tag()
	if _, err := tag.ReadData(); !nfc.IsNoPayloadError(err) {
		t.Errorf("ReadData without the key: %v, want a no-payload error", err)
	}
	if err := tag.WriteData(n4Message(t, "https://x.test")); !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData without the key: %v, want a read-only error", err)
	}
}

func TestNTAG424SessionSurvivesPollsAndUnchangedKeys(t *testing.T) {
	card := NTAG424(ntag424UID,
		NTAG424WithKeys(map[byte][]byte{0: n4Key0, 1: n4Key1}),
		NTAG424WithFileSettings(2, n4Protected(ntag424.CommMAC)))

	reader := NewEmulatedReader(t, card)
	reader.SetNTAG424Keys(n4Keys())

	msg := nfc.NewNDEFMessage()
	msg.AddRecord((&nfc.NDEFURI{Content: "https://davi.social/t/poll"}).ToRecord())
	if _, err := reader.WriteMessage(msg, nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if !card.NTAG424Authenticated() {
		t.Fatal("no session after a protected write")
	}

	for i := 0; i < 5; i++ {
		time.Sleep(60 * time.Millisecond)
		reader.SetNTAG424Keys(n4Keys())
	}
	if !card.NTAG424Authenticated() {
		t.Fatal("the session did not survive polls and unchanged keys")
	}
	counter, _ := card.NTAG424SessionCounter()

	if _, err := card.Tag().ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if after, _ := card.NTAG424SessionCounter(); after == counter {
		t.Error("the read did not reuse the open session")
	}
}

func TestNTAG424ReaderWriteAndLock(t *testing.T) {
	card := n4Provisioned(t, ntag424.FileSettings{
		ReadWrite: ntag424.AccessFree, Change: 0, Read: ntag424.AccessFree, Write: ntag424.AccessFree,
	})
	reader := NewEmulatedReader(t, card)
	reader.SetNTAG424Keys(n4Keys())

	msg := nfc.NewNDEFMessage()
	msg.AddRecord((&nfc.NDEFURI{Content: "https://davi.social/t/lock"}).ToRecord())
	if _, err := reader.WriteMessage(msg, nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if _, err := reader.Lock(context.Background(), "", ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	fs := card.NTAG424FileSettings(2)
	if fs.Write != ntag424.AccessNever || fs.ReadWrite != ntag424.AccessNever || fs.Change != 0 {
		t.Errorf("settings after lock = write %X rw %X change %X", fs.Write, fs.ReadWrite, fs.Change)
	}
	if err := card.Tag().WriteData(n4Message(t, "https://x.test")); !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData after lock: %v, want a read-only error", err)
	}
	if _, err := card.Tag().ReadData(); err != nil {
		t.Errorf("ReadData after lock: %v", err)
	}
}

func TestNTAG424LockNeedsTheChangeKey(t *testing.T) {
	card := NTAG424(ntag424UID)
	tag := card.Tag()
	if can, _ := tag.CanMakeReadOnly(); can {
		t.Error("CanMakeReadOnly with no keys held")
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly with no keys: %v, want not supported", err)
	}

	card.WithNTAG424Keys(nfc.NTAG424Keys{Master: make([]byte, 16)})
	if can, _ := tag.CanMakeReadOnly(); !can {
		t.Error("CanMakeReadOnly false with key 0 held")
	}
	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}

func TestNTAG424ConfigureSDMEndToEnd(t *testing.T) {
	zero := make([]byte, 16)
	card := NTAG424(ntag424UID).WithNTAG424Keys(nfc.NTAG424Keys{Master: zero})
	op := card.Tag().(nfc.NTAG424Operator)

	plan, err := ntag424.PlanSDM("https://davi.social/t?p={picc}&m={mac}", ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: 0, Change: 0,
		Read: ntag424.AccessFree, Write: 0, ReadWrite: ntag424.AccessNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := op.ConfigureSDM(plan)
	if err != nil {
		t.Fatalf("ConfigureSDM: %v", err)
	}
	if res.Tap == nil {
		t.Fatalf("result = %+v, want a verified tap", res)
	}
	if !card.NTAG424FileSettings(2).SDMEnabled {
		t.Error("SDM not enabled on the card")
	}

	// A second tap still verifies and the counter moves.
	data, err := card.Tag().ReadData()
	if err != nil {
		t.Fatal(err)
	}
	tap, err := ntag424.VerifyURLWith(n4URI(t, data), ntag424.Keys{MetaRead: zero, FileRead: zero},
		ntag424.Names{PICCData: []string{"p"}, MAC: []string{"m"}})
	if err != nil {
		t.Fatalf("VerifyURL: %v", err)
	}
	if tap.ReadCounter <= res.Tap.ReadCounter {
		t.Errorf("counter %d did not advance past %d", tap.ReadCounter, res.Tap.ReadCounter)
	}
}

func TestNTAG424OperatorCommands(t *testing.T) {
	zero := make([]byte, 16)
	card := NTAG424(ntag424UID, NTAG424WithKeyVersion(1, 7)).WithNTAG424Keys(nfc.NTAG424Keys{Master: zero})
	op := card.Tag().(nfc.NTAG424Operator)

	if v, err := op.GetKeyVersion(1); err != nil || v != 7 {
		t.Errorf("GetKeyVersion = %d, %v", v, err)
	}
	sig, err := op.ReadSig()
	if err != nil || !bytes.Equal(sig, NTAG424Signature) {
		t.Errorf("ReadSig = % X, %v", sig, err)
	}
	if v, err := op.GetKeyVersion(1); err != nil || v != 7 {
		t.Errorf("GetKeyVersion in session = %d, %v", v, err)
	}

	newKey := bytes.Repeat([]byte{0x5A}, 16)
	if err := op.ChangeKey(2, newKey, 3, 0); err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}
	if !bytes.Equal(card.NTAG424Key(2), newKey) || card.NTAG424KeyVersion(2) != 3 {
		t.Error("key 2 not changed")
	}
	if err := op.ChangeKey(0, newKey, 1, 0); err != nil {
		t.Fatalf("ChangeKey own key: %v", err)
	}
	if card.NTAG424Authenticated() {
		t.Error("session still open after changing its own key")
	}
}

func TestNTAG424RandomIDResolvesToTheRealUID(t *testing.T) {
	card := NTAG424(ntag424UID, NTAG424WithRandomID())
	tag := card.Tag()
	presented := tag.UID()
	if len(presented) != 8 || presented[:2] != "08" {
		t.Fatalf("presented UID %s, want 4 bytes opening 08", presented)
	}
	card.WithNTAG424Keys(nfc.NTAG424Keys{Master: make([]byte, 16)})

	tag.(interface{ ResolveUID() }).ResolveUID()
	if got := tag.UID(); got != ntag424UID {
		t.Errorf("UID after resolving = %s, want %s", got, ntag424UID)
	}
	aliases := tag.(interface{ UIDAliases() []string }).UIDAliases()
	if len(aliases) != 1 || aliases[0] != presented {
		t.Errorf("aliases = %v, want [%s]", aliases, presented)
	}
	if !tag.(nfc.NTAG424Operator).RandomID() {
		t.Error("RandomID false")
	}
}

func TestNTAG424RandomIDReaderPublishesAndRoutesByEither(t *testing.T) {
	card := NTAG424(ntag424UID, NTAG424WithRandomID())
	presented := card.Tag().UID()
	reader := NewEmulatedReader(t, card)
	reader.SetNTAG424Keys(nfc.NTAG424Keys{Master: make([]byte, 16)})

	for _, uid := range []string{ntag424UID, presented} {
		waitFor(t, 3*time.Second, func() bool {
			_, err := reader.Transceive(context.Background(), "", ntag424.GetKeyVersionPlain(1), uid)
			return err == nil
		}, "routing by "+uid)
	}
}

func TestNTAG424RandomIDWithDiversifiedKeys(t *testing.T) {
	ks := nfc.NTAG424Keys{
		Master: bytes.Repeat([]byte{0x33}, 16), SystemID: []byte("davi"), Diversify: true,
	}
	t.Run("no explicit slot", func(t *testing.T) {
		card := NTAG424(ntag424UID, NTAG424WithRandomID(), NTAG424WithKeySet(ks)).WithNTAG424Keys(ks)
		_, err := card.Tag().(nfc.NTAG424Operator).GetCardUID()
		if err == nil || !nfc.IsAuthError(err) {
			t.Errorf("GetCardUID = %v, want an auth error naming the missing slot", err)
		}
	})
	t.Run("explicit slot", func(t *testing.T) {
		withSlot := ks.Copy()
		withSlot.Slots = map[byte][]byte{3: bytes.Repeat([]byte{0x44}, 16)}
		card := NTAG424(ntag424UID, NTAG424WithRandomID(), NTAG424WithKeySet(withSlot)).WithNTAG424Keys(withSlot)
		op := card.Tag().(nfc.NTAG424Operator)
		uid, err := op.GetCardUID()
		if err != nil || nfc.BytesToHex(uid) != ntag424UID {
			t.Fatalf("GetCardUID = %X, %v", uid, err)
		}
		if _, err := op.ReadSig(); err != nil {
			t.Errorf("ReadSig under the diversified key 0: %v", err)
		}
	})
}

type n4Counting struct {
	inner nfc.CardTransport
	n     atomic.Int64
}

func (c *n4Counting) IsCardPresent() bool { return c.inner.IsCardPresent() }
func (c *n4Counting) Transceive(cmd []byte) ([]byte, error) {
	c.n.Add(1)
	return c.inner.Transceive(cmd)
}

func TestNTAG424AuthDelayStopsRetries(t *testing.T) {
	e := newNTAG424Emulator(ntag424UID, NTAG424WithFailedAuthLimit(true, 0, 1))
	ct := &n4Counting{inner: strictTransport{e}}
	tag := nfc.NewEmulatedTag(ct, ntag424UID, nfc.DetectedNTAG424)
	tag.(interface{ SetNTAG424Keys(nfc.NTAG424Keys) }).SetNTAG424Keys(nfc.NTAG424Keys{Master: make([]byte, 16)})
	op := tag.(nfc.NTAG424Operator)

	_, err := op.ReadSig()
	if !errors.Is(err, ntag424.ErrAuthDelay) {
		t.Fatalf("ReadSig = %v, want ErrAuthDelay", err)
	}
	sent := ct.n.Load()
	if _, err := op.ReadSig(); !errors.Is(err, ntag424.ErrAuthDelay) {
		t.Fatalf("second ReadSig = %v, want ErrAuthDelay", err)
	}
	if ct.n.Load() != sent {
		t.Errorf("the second attempt sent %d more APDUs", ct.n.Load()-sent)
	}
}

func TestNTAG424WrongKeyIsTriedOnce(t *testing.T) {
	card := NTAG424(ntag424UID, NTAG424WithKeys(map[byte][]byte{0: n4Key0}))
	card.WithNTAG424Keys(nfc.NTAG424Keys{Master: make([]byte, 16)})
	op := card.Tag().(nfc.NTAG424Operator)
	for i := 0; i < 3; i++ {
		if _, err := op.ReadSig(); !nfc.IsAuthError(err) {
			t.Fatalf("ReadSig = %v, want an auth error", err)
		}
	}
	if n := card.NTAG424FailedAuths(); n != 1 {
		t.Errorf("failed authentications = %d, want 1", n)
	}
}

func TestNTAG424CapabilitiesCarryTheDriversFacts(t *testing.T) {
	zero := make([]byte, 16)
	plan, err := ntag424.PlanSDM("https://davi.social/t?p={picc}&m={mac}", ntag424.SDMOptions{
		Read: ntag424.AccessFree, ReadWrite: ntag424.AccessNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := nfc.NTAG424Keys{Slots: map[byte][]byte{0: zero, 3: zero}}
	card := NTAG424(ntag424UID, NTAG424WithSDM(plan), NTAG424WithRandomID()).WithNTAG424Keys(keys)

	reader := NewEmulatedReader(t, card)
	reader.SetNTAG424Keys(keys)
	caps, err := reader.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if !caps.SDMEnabled || !caps.RandomID || caps.LRP {
		t.Errorf("caps = %+v, want SDM and random ID on, LRP off", caps)
	}
	if len(caps.KeysHeld) != 2 {
		t.Errorf("keysHeld = %v, want two slots", caps.KeysHeld)
	}

	raw, err := json.Marshal(nfc.TagCapabilities{CanRead: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"sdmEnabled", "keysHeld", "randomID", "lrp"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("zero value carries %q: %s", field, raw)
		}
	}
}

func TestNTAG424ReadOriginality(t *testing.T) {
	uid, err := hex.DecodeString(ntag424UID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(uid []byte) []byte {
		r, s, err := ecdsa.Sign(rand.Reader, key, uid)
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, ntag424.SigSize)
		r.FillBytes(sig[:ntag424.SigSize/2])
		s.FillBytes(sig[ntag424.SigSize/2:])
		return sig
	}
	keys := nfc.NTAG424Keys{Master: make([]byte, 16)}
	run := func(name string, sig []byte, opts ...NTAG424Option) bool {
		t.Helper()
		opts = append(opts, NTAG424WithSignature(sig))
		op := NTAG424(ntag424UID, opts...).WithNTAG424Keys(keys).Tag().(nfc.NTAG424Operator)
		got, genuine, err := op.ReadOriginality(&key.PublicKey)
		if err != nil || !bytes.Equal(got, sig) {
			t.Fatalf("%s: ReadOriginality = % X, %v", name, got, err)
		}
		return genuine
	}

	good := sign(uid)
	if !run("genuine", good) {
		t.Error("a signature over the UID is not genuine")
	}
	if !run("random ID", good, NTAG424WithRandomID()) {
		t.Error("a random-ID card is not verified against its real UID")
	}
	tampered := append([]byte(nil), good...)
	tampered[3] ^= 0x80
	if run("tampered signature", tampered) {
		t.Error("a tampered signature is genuine")
	}
	other := append([]byte(nil), uid...)
	other[0] ^= 0x01
	if run("other UID", sign(other)) {
		t.Error("a signature over another UID is genuine")
	}
	if run("default signature", NTAG424Signature) {
		t.Error("the emulator's placeholder signature is genuine")
	}
}
