package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// TestKey is the key a non-master slot is changed to and back, in the LRP
// procedure. It is a throwaway value that guards nothing.
var TestKey = bytes.Repeat([]byte{0xA5}, ntag424.KeySize)

// LRPConfig is what RunLRP needs.
type LRPConfig struct {
	Sup *nfc.Supervisor
	Rec *fixture.Recorder
	Tag Tag

	// Keys are the NTAG 424 keys the agent authenticates with. The procedure
	// sets AllowLRP itself, off for the refusal check and on for the rest.
	Keys nfc.NTAG424Keys

	// EnableUID is the UID of the one tag the procedure may switch to LRP,
	// permanently. A tag on the reader with any other UID is never switched.
	EnableUID string

	// IncludeKeys records the AES keys in the fixture so it can be replayed.
	IncludeKeys bool

	// KeepSDM leaves the SDM configuration on the tag, so it can be tapped in
	// a browser, instead of restoring the settings and message it had.
	KeepSDM bool

	// KeySlot is the non-master slot changed to TestKey and back. Zero means
	// 3.
	KeySlot byte

	// Warn is told about what is about to be done to the tag that cannot be
	// undone. It defaults to the test log; a hardware test points it at standard
	// error so it shows without -v.
	Warn func(format string, args ...any)

	// SDMMeta and SDMFile are the key numbers the SDM configuration uses to
	// encrypt PICCData and derive the MAC. Zero values mean 2 and 3.
	SDMMeta, SDMFile byte
}

type ndefFile interface {
	ReadData() ([]byte, error)
	WriteData([]byte) error
}

type lrpRun struct {
	tb  TB
	ctx context.Context
	cfg LRPConfig
	out *fixture.LRP

	allow, deny nfc.NTAG424Keys
	urlStore    ntag424.MemoryCounterStore

	orig2     *ntag424.FileSettings
	origNDEF  []byte
	haveNDEF  bool
	keySlot   byte
	metaKey   byte
	fileKey   byte
	cardIsLRP bool
}

// RunLRP is the procedure of issue #99, for an NTAG 424 DNA on the reader.
//
// With the keys it was given it records a baseline on the AES card, switches
// the one tag named by EnableUID to LRP (or carries on if the tag already is
// LRP), and then checks, through Supervisor.WithNTAG424Tag, that capabilities
// report LRP, that the driver refuses the card while AllowLRP is off, and, with
// it on: GetCardUID, protected NDEF read and write in MAC and Full mode, a key
// change on a non-master slot and back, and ConfigureSDM with an LRP plan, whose
// URLs are verified with ntag424.VerifyURLFresh and Keys.LRP. File settings,
// the NDEF message and the key it changed are put back unless KeepSDM is set.
//
// Every exchange is recorded into out, which the caller writes whether or not
// the procedure passed: a failing run is the most useful capture.
func RunLRP(ctx context.Context, tb TB, cfg LRPConfig, out *fixture.LRP) {
	tb.Helper()

	info, _ := cfg.Rec.Info()
	out.Version, out.Kind = fixture.Version, fixture.KindLRP
	out.Reader, out.ATR, out.UID = info.Name, fixture.Hex(info.ATR), cfg.Tag.UID

	h := &lrpRun{tb: tb, ctx: ctx, cfg: cfg, out: out, keySlot: 3, metaKey: 2, fileKey: 3}
	if cfg.KeySlot != 0 {
		h.keySlot = cfg.KeySlot
	}
	if cfg.SDMMeta != 0 {
		h.metaKey = cfg.SDMMeta
	}
	if cfg.SDMFile != 0 {
		h.fileKey = cfg.SDMFile
	}
	h.allow, h.deny = cfg.Keys.Copy(), cfg.Keys.Copy()
	h.allow.AllowLRP, h.deny.AllowLRP = true, false

	if !h.baseline() {
		return
	}
	if !h.enableLRP() {
		return
	}
	h.checkCapabilities()
	h.checkRefusal()

	h.cfg.Sup.SetNTAG424Keys(h.allow)
	h.checkSession()
	h.protectedNDEF()
	h.changeKey()
	h.sdm()
	h.restore()
}

// step runs fn as one tag operation and records the exchanges it caused.
func (h *lrpRun) step(name, suite string, fn func(op nfc.NTAG424Operator) error) error {
	h.tb.Helper()
	mark := h.cfg.Rec.Mark()
	err := h.cfg.Sup.WithNTAG424Tag(h.ctx, h.cfg.Tag.Device, h.cfg.Tag.UID, fn)
	s := fixture.LRPStep{Name: name, Suite: suite, Wire: h.cfg.Rec.Since(mark)}
	if err != nil {
		s.Error = err.Error()
	}
	h.out.Steps = append(h.out.Steps, s)
	return err
}

func (h *lrpRun) last() *fixture.LRPStep { return &h.out.Steps[len(h.out.Steps)-1] }

func (h *lrpRun) fail(name string, err error) {
	h.tb.Helper()
	h.tb.Errorf("%s: %v", name, err)
}

// baseline reads what an AES card reports, and finds out whether the card is
// already in LRP mode: a card that refuses the AES key with ErrLRP is.
func (h *lrpRun) baseline() bool {
	h.tb.Helper()
	h.cfg.Sup.SetNTAG424Keys(h.deny)

	var settings [4]*ntag424.FileSettings
	var versions [5]byte
	err := h.step("baseline", "", func(op nfc.NTAG424Operator) error {
		if h.cfg.IncludeKeys {
			h.out.Keys = map[string]string{}
			src, ok := op.(nfc.NTAG424KeySource)
			if !ok {
				return errors.New("the driver does not name its keys")
			}
			for n := byte(0); n <= 4; n++ {
				key, err := src.ConfiguredKey(n)
				if err != nil {
					return fmt.Errorf("key %d: %w", n, err)
				}
				h.out.Keys[fmt.Sprint(n)] = fixture.Hex(key)
			}
		}
		for n := byte(0); n <= 4; n++ {
			v, err := op.GetKeyVersion(n)
			if err != nil {
				return fmt.Errorf("getKeyVersion %d: %w", n, err)
			}
			versions[n] = v
		}
		for n := byte(1); n <= 3; n++ {
			fs, err := op.GetFileSettings(n)
			if err != nil {
				return fmt.Errorf("getFileSettings %d: %w", n, err)
			}
			settings[n] = fs
		}
		if _, err := op.ReadSig(); err != nil {
			return fmt.Errorf("authenticate with the key held for slot 0: %w", err)
		}
		return nil
	})

	switch {
	case err == nil:
		h.last().Suite = fixture.SuiteAES
		h.tb.Logf("baseline on an AES card: key versions %v", versions)
		for n := 1; n <= 3; n++ {
			h.tb.Logf("baseline getFileSettings(%d): %+v", n, *settings[n])
		}
		return true
	case errors.Is(err, ntag424.ErrLRP):
		h.cardIsLRP = true
		h.last().Note = "the card is already in LRP mode, so there is no AES baseline"
		h.tb.Logf("the tag is already in LRP mode (switched by an earlier run); skipping the AES baseline and the switch")
		return true
	default:
		h.last().Suite = fixture.SuiteAES
		h.fail("baseline", err)
		return false
	}
}

// enableLRP switches the tag, only when it is the tag EnableUID names.
func (h *lrpRun) enableLRP() bool {
	h.tb.Helper()
	if h.cardIsLRP {
		return true
	}

	uid := h.cfg.Tag.UID
	switch {
	case h.cfg.EnableUID == "":
		h.tb.Skipf("the tag %s is on AES and DAVI_HW_ENABLE_LRP_UID is not set. The LRP checks need a tag in LRP mode; set DAVI_HW_ENABLE_LRP_UID=%s to switch THIS tag PERMANENTLY", uid, uid)
		return false
	case !strings.EqualFold(h.cfg.EnableUID, uid):
		h.tb.Skipf("the tag on the reader is %s, but DAVI_HW_ENABLE_LRP_UID names %s. Not switching a tag that was not named", uid, h.cfg.EnableUID)
		return false
	}

	warn := h.cfg.Warn
	if warn == nil {
		warn = h.tb.Logf
	}
	warn("WARNING: switching tag %s to LRP now. This is permanent: the tag will refuse AES authentication for the rest of its life", uid)
	err := h.step("switch-to-lrp", fixture.SuiteAES, func(op nfc.NTAG424Operator) error {
		sw, ok := op.(nfc.NTAG424LRPSwitch)
		if !ok {
			return errors.New("the driver offers no LRP switch")
		}
		return sw.EnableLRP()
	})
	if err != nil {
		h.fail("EnableLRP", err)
		return false
	}
	h.cardIsLRP = true
	return true
}

func (h *lrpRun) checkCapabilities() {
	h.tb.Helper()
	caps, err := h.cfg.Sup.Capabilities(h.ctx, h.cfg.Tag.Device, h.cfg.Tag.UID)
	if err != nil {
		h.fail("capabilities", err)
		return
	}
	if !caps.LRP {
		h.tb.Errorf("capabilities do not report lrp:true for a tag in LRP mode: %+v", *caps)
	}
}

func (h *lrpRun) checkRefusal() {
	h.tb.Helper()
	h.cfg.Sup.SetNTAG424Keys(h.deny)
	err := h.step("refused-without-allowlrp", "", func(op nfc.NTAG424Operator) error {
		_, err := op.ReadSig()
		return err
	})
	switch {
	case err == nil:
		h.tb.Errorf("the driver authenticated to an LRP card with AllowLRP off")
	case !nfc.IsAuthError(err) || !errors.Is(err, ntag424.ErrLRP):
		h.tb.Errorf("with AllowLRP off the driver answered %v, want an auth error naming ErrLRP", err)
	}
}

// checkSession runs GetCardUID and the reads that need a session.
func (h *lrpRun) checkSession() {
	h.tb.Helper()
	err := h.step("authenticate-and-read", fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		if _, err := op.ReadSig(); err != nil {
			return fmt.Errorf("authenticate: %w", err)
		}
		uid, err := op.GetCardUID()
		if err != nil {
			return fmt.Errorf("getCardUID: %w", err)
		}
		if got := fixture.Hex(uid); !op.RandomID() && !strings.EqualFold(got, h.cfg.Tag.UID) {
			return fmt.Errorf("getCardUID = %s, the tag scanned as %s", got, h.cfg.Tag.UID)
		}
		for n := byte(0); n <= 4; n++ {
			if _, err := op.GetKeyVersion(n); err != nil {
				return fmt.Errorf("getKeyVersion %d in a session: %w", n, err)
			}
		}
		fs, err := op.GetFileSettings(ntag424.NDEFFileNo)
		if err != nil {
			return fmt.Errorf("getFileSettings in a session: %w", err)
		}
		h.orig2 = fs
		return nil
	})
	if err != nil {
		h.fail("session", err)
	}
}

// ndefMessage is the message the protected read and write round trips.
func ndefMessage(url string) ([]byte, error) {
	msg, err := (&nfc.NDEFMessageBuilder{Records: []nfc.NDEFRecordBuilder{&nfc.NDEFURI{Content: url}}}).Build()
	if err != nil {
		return nil, err
	}
	return msg.Encode()
}

// protectedNDEF writes and reads the NDEF file in MAC and Full mode, setting
// the file's settings to need them when they do not already.
func (h *lrpRun) protectedNDEF() {
	h.tb.Helper()
	if h.orig2 == nil {
		h.tb.Errorf("no file settings were read, so protected NDEF cannot be set up")
		return
	}

	// Keep the message the tag holds, to put back.
	_ = h.step("ndef-save-original", fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		f, ok := op.(ndefFile)
		if !ok {
			return errors.New("the driver offers no NDEF read")
		}
		data, err := f.ReadData()
		if err != nil {
			return err
		}
		h.origNDEF, h.haveNDEF = data, true
		return nil
	})
	if !h.haveNDEF {
		h.last().Note = "the original message could not be read, so it will not be put back"
	}

	for _, mode := range []ntag424.CommMode{ntag424.CommMAC, ntag424.CommFull} {
		name := "ndef-mac"
		if mode == ntag424.CommFull {
			name = "ndef-full"
		}
		want, err := ndefMessage("https://example.com/davi-hw/" + name)
		if err != nil {
			h.fail(name, err)
			continue
		}

		settings := *h.orig2
		settings.CommMode = mode
		settings.ReadWrite, settings.Read, settings.Write = 0, 0, 0
		err = h.step(name, fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
			if err := op.ChangeFileSettings(ntag424.NDEFFileNo, settings); err != nil {
				return fmt.Errorf("changeFileSettings to %s: %w", name, err)
			}
			f, ok := op.(ndefFile)
			if !ok {
				return errors.New("the driver offers no NDEF write")
			}
			if err := f.WriteData(want); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			got, err := f.ReadData()
			if err != nil {
				return fmt.Errorf("read: %w", err)
			}
			if !bytes.Equal(got, want) {
				return fmt.Errorf("read back % X, wrote % X", got, want)
			}
			return nil
		})
		if err != nil {
			h.fail(name, err)
		}
	}
}

// changeKey moves a non-master slot to TestKey and back. The driver does not
// update its own key set, so the held key for the slot is set to match the card
// between the two.
func (h *lrpRun) changeKey() {
	h.tb.Helper()
	slot := h.keySlot

	var old []byte
	var version byte
	changed := false
	err := h.step("change-key-to-test", fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		src, ok := op.(nfc.NTAG424KeySource)
		if !ok {
			return errors.New("the driver does not name its keys")
		}
		var err error
		if old, err = src.ConfiguredKey(slot); err != nil {
			return err
		}
		if version, err = op.GetKeyVersion(slot); err != nil {
			return err
		}
		testVersion := version + 1
		if err := op.ChangeKey(slot, TestKey, testVersion, 0); err != nil {
			return fmt.Errorf("changeKey %d: %w", slot, err)
		}
		changed = true
		got, err := op.GetKeyVersion(slot)
		if err != nil {
			return fmt.Errorf("getKeyVersion %d after the change: %w", slot, err)
		}
		if got != testVersion {
			return fmt.Errorf("key %d is at version %d after the change, want %d", slot, got, testVersion)
		}
		return nil
	})
	if changed && h.cfg.IncludeKeys {
		h.last().KeysAfter = map[string]string{fmt.Sprint(slot): fixture.Hex(TestKey)}
	}
	if err != nil {
		h.fail("change-key", err)
	}
	if !changed {
		return
	}

	moved := h.allow.Copy()
	if moved.Slots == nil {
		moved.Slots = map[byte][]byte{}
	}
	moved.Slots[slot] = TestKey
	h.cfg.Sup.SetNTAG424Keys(moved)

	err = h.step("change-key-back", fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		return op.ChangeKey(slot, old, version, 0)
	})
	h.cfg.Sup.SetNTAG424Keys(h.allow)
	if err != nil {
		h.fail("change-key-back", err)
		h.tb.Errorf("KEY SLOT %d STILL HOLDS THE TEST KEY %s. Set it back with the key held for it in your key file", slot, fixture.Hex(TestKey))
		return
	}
	if h.cfg.IncludeKeys {
		h.last().KeysAfter = map[string]string{fmt.Sprint(slot): fixture.Hex(old)}
	}
}

// sdm configures SDM twice, with the PICCData encrypted and in the clear, and
// verifies what the tag mirrors on three taps of each.
func (h *lrpRun) sdm() {
	h.tb.Helper()
	opts := ntag424.SDMOptions{
		MetaRead: h.metaKey, FileRead: h.fileKey, CounterRet: ntag424.AccessNever,
		Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0, LRP: true,
	}
	meta := int(h.metaKey)

	h.sdmPlan("sdm-encrypted-picc", "https://example.com/davi-hw/lrp?picc={picc}&mac={mac}", opts, &meta)
	h.sdmPlan("sdm-plain-mirror", "https://example.com/davi-hw/lrp?uid={uid}&ctr={ctr}&mac={mac}", opts, nil)
	h.tb.Logf("encrypted file data ({enc}) is not offered for a tag in LRP mode, so it is not exercised")
}

func (h *lrpRun) sdmPlan(name, template string, opts ntag424.SDMOptions, metaKey *int) {
	h.tb.Helper()
	plan, err := ntag424.PlanSDM(template, opts)
	if err != nil {
		h.fail(name, err)
		return
	}

	var urls []string
	var keys ntag424.Keys
	err = h.step(name, fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		res, err := op.ConfigureSDM(plan)
		if res != nil && res.URL != "" {
			urls = append(urls, res.URL)
		}
		if err != nil {
			return err
		}
		src, ok := op.(nfc.NTAG424KeySource)
		f, ok2 := op.(ndefFile)
		if !ok || !ok2 {
			return errors.New("the driver offers no key or NDEF access")
		}
		keys = ntag424.Keys{LRP: true}
		if metaKey != nil {
			if keys.MetaRead, err = src.ConfiguredKey(byte(*metaKey)); err != nil {
				return err
			}
		}
		if keys.FileRead, err = src.ConfiguredKey(opts.FileRead); err != nil {
			return err
		}
		for tap := 0; tap < 2; tap++ {
			data, err := f.ReadData()
			if err != nil {
				return fmt.Errorf("tap %d: %w", tap+2, err)
			}
			msg, err := nfc.DecodeNDEF(data)
			if err != nil {
				return fmt.Errorf("tap %d: %w", tap+2, err)
			}
			url, err := msg.GetURI()
			if err != nil {
				return fmt.Errorf("tap %d: %w", tap+2, err)
			}
			urls = append(urls, url)
		}
		return nil
	})
	if err != nil {
		h.fail(name, err)
		if len(urls) > 0 {
			h.last().Note = "URLs read before the failure, unverified: " + strings.Join(urls, " ")
		}
		return
	}

	for i, u := range urls {
		tap, err := ntag424.VerifyURLFresh(u, keys, &h.urlStore)
		if err != nil {
			h.fail(fmt.Sprintf("%s tap %d: verify %s", name, i+1, u), err)
			continue
		}
		h.tb.Logf("%s tap %d: %s -> uid %s counter %d", name, i+1, u, tap.UIDString(), tap.ReadCounter)
		h.out.URLs = append(h.out.URLs, fixture.SUNURL{
			Name: fmt.Sprintf("%s-tap-%d", name, i+1), URL: u,
			MetaReadKey: metaKey, FileReadKey: int(opts.FileRead), LRP: true, Counter: tap.ReadCounter,
		})
	}
	if len(urls) > 0 {
		if _, err := ntag424.VerifyURLFresh(urls[len(urls)-1], keys, &h.urlStore); !errors.Is(err, ntag424.ErrReplay) {
			h.tb.Errorf("%s: a repeated URL was not refused as a replay: %v", name, err)
		}
	}
}

// restore puts back the file settings and message the tag had.
func (h *lrpRun) restore() {
	h.tb.Helper()
	if h.cfg.KeepSDM {
		h.tb.Logf("DAVI_HW_KEEP_SDM is set: the SDM configuration stays on the tag")
		return
	}
	if h.orig2 == nil {
		return
	}
	err := h.step("restore-file-settings", fixture.SuiteLRP, func(op nfc.NTAG424Operator) error {
		if err := op.ChangeFileSettings(ntag424.NDEFFileNo, *h.orig2); err != nil {
			return err
		}
		if !h.haveNDEF {
			return nil
		}
		f, ok := op.(ndefFile)
		if !ok {
			return nil
		}
		return f.WriteData(h.origNDEF)
	})
	if err != nil {
		h.fail("restore", err)
	}
}
