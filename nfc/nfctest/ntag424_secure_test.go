package nfctest

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

const secureUID = "04A1B2C3D4E5F6"

var zeroKey = make([]byte, 16)

func send(t *testing.T, c *EmulatedCard, apdu []byte) []byte {
	t.Helper()
	resp, err := c.Transceive(apdu)
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	return resp
}

// authenticate runs a full EV2 exchange against the card.
func authenticate(t *testing.T, c *EmulatedCard, mode ntag424.AuthMode, keyNo byte, key, ti []byte) (*ntag424.Session, error) {
	t.Helper()
	a, err := ntag424.NewAuthenticator(mode, keyNo, key, ti)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Challenge(send(t, c, a.Command()))
	if err != nil {
		return nil, err
	}
	return a.Finish(send(t, c, second))
}

func mustAuth(t *testing.T, c *EmulatedCard, keyNo byte, key []byte) *ntag424.Session {
	t.Helper()
	s, err := authenticate(t, c, ntag424.AuthFirst, keyNo, key, nil)
	if err != nil {
		t.Fatalf("authenticate key %d: %v", keyNo, err)
	}
	return s
}

func TestNTAG424SecureAuthenticates(t *testing.T) {
	c := NTAG424(secureUID)
	if c.NTAG424Authenticated() {
		t.Fatal("authenticated before any command")
	}
	s := mustAuth(t, c, 0, zeroKey)
	if !c.NTAG424Authenticated() {
		t.Fatal("not authenticated after AuthenticateEV2First")
	}
	if k, _ := c.NTAG424AuthKey(); k != 0 {
		t.Errorf("auth key = %d, want 0", k)
	}

	ns, err := authenticate(t, c, ntag424.AuthNonFirst, 0, zeroKey, s.TI())
	if err != nil {
		t.Fatalf("NonFirst: %v", err)
	}
	if !bytes.Equal(ns.TI(), s.TI()) {
		t.Error("NonFirst changed the transaction identifier")
	}
	cmd, _ := ntag424.GetKeyVersion(ns, 1)
	if _, err := ntag424.ParseKeyVersion(ns, send(t, c, cmd)); err != nil {
		t.Errorf("command after NonFirst: %v", err)
	}
}

func TestNTAG424SecureFailedAuthCounterAndDelay(t *testing.T) {
	c := NTAG424(secureUID, NTAG424WithFailedAuthLimit(true, 3, 1))
	wrong := bytes.Repeat([]byte{0x77}, 16)
	for i := 1; i <= 3; i++ {
		if _, err := authenticate(t, c, ntag424.AuthFirst, 0, wrong, nil); err == nil {
			t.Fatal("authenticated with the wrong key")
		}
		if got := c.NTAG424FailedAuths(); got != i {
			t.Fatalf("failed auths = %d, want %d", got, i)
		}
	}
	a, _ := ntag424.NewAuthenticator(ntag424.AuthFirst, 0, zeroKey, nil)
	resp := send(t, c, a.Command())
	if sw := swOf(resp, nil); sw != 0x91AD {
		t.Fatalf("SW at the limit = %04X, want 91AD", sw)
	}
	if _, err := ntag424.CheckResponse(nil, resp, ntag424.CommPlain); !errors.Is(err, ntag424.ErrAuthDelay) {
		t.Errorf("error = %v, want ErrAuthDelay", err)
	}
}

func TestNTAG424SecureSuccessLowersFailedAuths(t *testing.T) {
	c := NTAG424(secureUID, NTAG424WithFailedAuthLimit(true, 10, 1))
	bad := bytes.Repeat([]byte{1}, 16)
	_, _ = authenticate(t, c, ntag424.AuthFirst, 1, bad, nil)
	_, _ = authenticate(t, c, ntag424.AuthFirst, 1, bad, nil)
	mustAuth(t, c, 0, zeroKey)
	if got := c.NTAG424FailedAuths(); got != 1 {
		t.Errorf("failed auths after a success = %d, want 1", got)
	}
}

func TestNTAG424SecureConfiguresFailedAuthCounter(t *testing.T) {
	c := NTAG424(secureUID)
	s := mustAuth(t, c, 0, zeroKey)
	cmd, _ := ntag424.SetFailedAuthCounter(s, true, 2, 1)
	if err := ntag424.ParseSetConfiguration(s, send(t, c, cmd)); err != nil {
		t.Fatal(err)
	}
	wrong := bytes.Repeat([]byte{9}, 16)
	_, _ = authenticate(t, c, ntag424.AuthFirst, 0, wrong, nil)
	_, _ = authenticate(t, c, ntag424.AuthFirst, 0, wrong, nil)
	a, _ := ntag424.NewAuthenticator(ntag424.AuthFirst, 0, zeroKey, nil)
	if sw := swOf(send(t, c, a.Command()), nil); sw != 0x91AD {
		t.Errorf("SW = %04X, want 91AD", sw)
	}
}

func TestNTAG424SecureFactoryFileSettings(t *testing.T) {
	c := NTAG424(secureUID)

	want := map[byte]struct {
		size                    uint32
		comm                    ntag424.CommMode
		rw, change, read, write byte
	}{
		1: {32, ntag424.CommPlain, 0, 0, ntag424.AccessFree, 0},
		2: {256, ntag424.CommPlain, ntag424.AccessFree, 0, ntag424.AccessFree, ntag424.AccessFree},
		3: {128, ntag424.CommFull, 3, 0, 2, 3},
	}

	fs, err := ntag424.ParseFileSettingsResponse(nil, send(t, c, ntag424.GetFileSettingsPlain(2)))
	if err != nil {
		t.Fatalf("plain GetFileSettings: %v", err)
	}
	if fs.FileSize != 256 || fs.SDMEnabled {
		t.Errorf("file 02 = %+v", fs)
	}

	if _, err := ntag424.ParseFileSettingsResponse(nil, send(t, c, ntag424.GetFileSettingsPlain(3))); !errors.Is(err, ntag424.ErrSessionLost) {
		t.Errorf("plain GetFileSettings on file 03: %v, want a refusal", err)
	}

	s := mustAuth(t, c, 0, zeroKey)
	for n, w := range want {
		cmd, _ := ntag424.GetFileSettings(s, n)
		fs, err := ntag424.ParseFileSettingsResponse(s, send(t, c, cmd))
		if err != nil {
			t.Fatalf("file %d: %v", n, err)
		}
		if fs.FileSize != w.size || fs.CommMode != w.comm || fs.ReadWrite != w.rw ||
			fs.Change != w.change || fs.Read != w.read || fs.Write != w.write {
			t.Errorf("file %d = %+v, want %+v", n, fs, w)
		}
	}

	cmd, _ := ntag424.GetFileSettings(s, 9)
	if _, err := ntag424.ParseFileSettingsResponse(s, send(t, c, cmd)); err == nil {
		t.Error("GetFileSettings on a missing file succeeded")
	}
}

func TestNTAG424SecureKeyVersionsAndChangeKey(t *testing.T) {
	c := NTAG424(secureUID, NTAG424WithKeyVersion(2, 7))

	v, err := ntag424.ParseKeyVersion(nil, send(t, c, ntag424.GetKeyVersionPlain(2)))
	if err != nil || v != 7 {
		t.Fatalf("plain key version = %d, %v; want 7", v, err)
	}

	s := mustAuth(t, c, 0, zeroKey)
	newKey1 := bytes.Repeat([]byte{0xA1}, 16)
	cmd, _ := ntag424.ChangeKey(s, 1, 0, zeroKey, newKey1, 0x05)
	if err := ntag424.CheckChangeKeyResponse(s, send(t, c, cmd), 1, 0); err != nil {
		t.Fatalf("ChangeKey 1: %v", err)
	}
	if !bytes.Equal(c.NTAG424Key(1), newKey1) || c.NTAG424KeyVersion(1) != 5 {
		t.Error("key 1 not stored")
	}

	cmd, _ = ntag424.GetKeyVersion(s, 1)
	if v, err := ntag424.ParseKeyVersion(s, send(t, c, cmd)); err != nil || v != 5 {
		t.Errorf("key version in session = %d, %v", v, err)
	}

	cmd, _ = ntag424.ChangeKey(s, 2, 0, bytes.Repeat([]byte{0xEE}, 16), newKey1, 1)
	if sw := swOf(send(t, c, cmd), nil); sw != 0x911E {
		t.Errorf("ChangeKey with the wrong old key: %04X, want 911E", sw)
	}
	if c.NTAG424Authenticated() {
		t.Error("session survived an integrity failure")
	}

	s = mustAuth(t, c, 0, zeroKey)
	newKey0 := bytes.Repeat([]byte{0xB0}, 16)
	cmd, _ = ntag424.ChangeKey(s, 0, 0, nil, newKey0, 0x01)
	if err := ntag424.CheckChangeKeyResponse(s, send(t, c, cmd), 0, 0); err != nil {
		t.Fatalf("ChangeKey 0: %v", err)
	}
	if c.NTAG424Authenticated() {
		t.Error("session survived changing its own key")
	}
	if _, err := authenticate(t, c, ntag424.AuthFirst, 0, zeroKey, nil); err == nil {
		t.Error("old key 0 still authenticates")
	}
	mustAuth(t, c, 0, newKey0)
}

func TestNTAG424SecureGetCardUIDCountersSigConfig(t *testing.T) {
	c := NTAG424(secureUID, NTAG424WithTagTamper('C', 'O'))
	s := mustAuth(t, c, 0, zeroKey)

	cmd, _ := ntag424.GetCardUID(s)
	uid, err := ntag424.ParseCardUID(s, send(t, c, cmd))
	if err != nil || nfc.BytesToHex(uid) != secureUID {
		t.Fatalf("GetCardUID = %X, %v", uid, err)
	}

	cmd, _ = ntag424.ReadSig(s)
	sig, err := ntag424.ParseReadSig(s, send(t, c, cmd))
	if err != nil || !bytes.Equal(sig, NTAG424Signature) {
		t.Fatalf("ReadSig = %X, %v", sig, err)
	}

	cmd, _ = ntag424.GetTTStatus(s)
	tt, err := ntag424.ParseTTStatus(s, send(t, c, cmd))
	if err != nil || tt.Permanent != 'C' || tt.Current != 'O' {
		t.Fatalf("GetTTStatus = %+v, %v", tt, err)
	}

	cmd, _ = ntag424.SetRandomID(s, true)
	if err := ntag424.ParseSetConfiguration(s, send(t, c, cmd)); err != nil {
		t.Fatal(err)
	}
	if !c.NTAG424RandomID() {
		t.Error("random ID not enabled")
	}

	cmd, _ = ntag424.GetFileCounters(s, 2)
	if _, err := ntag424.ParseFileCounters(s, send(t, c, cmd)); !errors.Is(err, ntag424.ErrPermissionDenied) {
		t.Errorf("GetFileCounters without SDM: %v", err)
	}
}

func TestNTAG424SecureDataInEveryMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode ntag424.CommMode
	}{{"plain", ntag424.CommPlain}, {"mac", ntag424.CommMAC}, {"full", ntag424.CommFull}} {
		t.Run(tc.name, func(t *testing.T) {
			key3 := bytes.Repeat([]byte{0x33}, 16)
			fs := ntag424.FileSettings{CommMode: tc.mode, ReadWrite: 3, Change: 0, Read: 3, Write: 3}
			c := NTAG424(secureUID, NTAG424WithKeys(map[byte][]byte{3: key3}), NTAG424WithFileSettings(3, fs))

			plain, _ := ntag424.ReadDataPlain(3, 0, 4)
			if _, err := ntag424.ParseReadData(nil, send(t, c, plain), tc.mode); err == nil {
				t.Error("file 03 read without a session")
			}
			if sw := swOf(send(t, c, nfc.SelectFileByAIDAPDU(type4NDEFAppAID)), nil); sw != 0x9000 {
				t.Fatal("select app")
			}
			send(t, c, nfc.SelectFileAPDU([]byte{0xE1, 0x05}))
			if sw := swOf(send(t, c, nfc.ReadBinaryExtAPDU(0, 4)), nil); sw != 0x6982 {
				t.Errorf("ISO read of file 03 = %04X, want 6982", sw)
			}

			s := mustAuth(t, c, 3, key3)
			data := []byte("hello, ntag424 proprietary file")
			cmd, err := ntag424.WriteData(s, 3, 10, data, tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			if err := ntag424.ParseWriteData(s, send(t, c, cmd), tc.mode); err != nil {
				t.Fatalf("WriteData: %v", err)
			}
			cmd, _ = ntag424.ReadData(s, 3, 10, uint32(len(data)), tc.mode)
			got, err := ntag424.ParseReadData(s, send(t, c, cmd), tc.mode)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("ReadData = %q, %v", got, err)
			}
			if !bytes.Equal(c.NTAG424FileData(3)[10:10+len(data)], data) {
				t.Error("file not written")
			}

			cmd, _ = ntag424.ReadData(s, 3, 120, 20, tc.mode)
			if _, err := ntag424.ParseReadData(s, send(t, c, cmd), tc.mode); err == nil {
				t.Error("read past the end of the file succeeded")
			}
		})
	}
}

func TestNTAG424SecureAccessRights(t *testing.T) {
	c := NTAG424(secureUID)

	cc, _ := ntag424.ReadDataPlain(1, 0, 15)
	if _, err := ntag424.ParseReadData(nil, send(t, c, cc), ntag424.CommPlain); err != nil {
		t.Errorf("plain read of the CC: %v", err)
	}
	w, _ := ntag424.WriteDataPlain(1, 0, []byte{1})
	if err := ntag424.ParseWriteData(nil, send(t, c, w), ntag424.CommPlain); err == nil {
		t.Error("plain write of the CC succeeded")
	}

	s := mustAuth(t, c, 3, zeroKey)
	enc, _ := ntag424.FileSettings{CommMode: ntag424.CommPlain, ReadWrite: 0xE, Change: 0, Read: 0xE, Write: 0xE}.Encode()
	cmd, _ := ntag424.ChangeFileSettings(s, 2, enc)
	if _, err := ntag424.CheckResponse(s, send(t, c, cmd), ntag424.CommFull); !errors.Is(err, ntag424.ErrPermissionDenied) {
		t.Errorf("ChangeFileSettings under key 3: %v, want permission denied", err)
	}
}

func TestNTAG424SecureChangeFileSettingsRoundTrip(t *testing.T) {
	c := NTAG424(secureUID)
	s := mustAuth(t, c, 0, zeroKey)

	want := ntag424.FileSettings{CommMode: ntag424.CommMAC, ReadWrite: 2, Change: 0, Read: 2, Write: 4}
	enc, err := want.Encode()
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := ntag424.ChangeFileSettings(s, 3, enc)
	if _, err := ntag424.CheckResponse(s, send(t, c, cmd), ntag424.CommFull); err != nil {
		t.Fatalf("ChangeFileSettings: %v", err)
	}

	cmd, _ = ntag424.GetFileSettings(s, 3)
	got, err := ntag424.ParseFileSettingsResponse(s, send(t, c, cmd))
	if err != nil {
		t.Fatal(err)
	}
	if got.CommMode != want.CommMode || got.Read != 2 || got.Write != 4 || got.ReadWrite != 2 || got.FileSize != 128 {
		t.Errorf("settings = %+v", got)
	}

	bad := want
	bad.SDMEnabled, bad.ASCIIEncoding, bad.MirrorUID = true, true, true
	bad.SDMMetaRead, bad.SDMFileRead, bad.SDMCounterRet = ntag424.AccessFree, ntag424.AccessNever, 0
	enc, _ = bad.Encode()
	cmd, _ = ntag424.ChangeFileSettings(s, 3, enc)
	if _, err := ntag424.CheckResponse(s, send(t, c, cmd), ntag424.CommFull); err == nil {
		t.Error("SDM accepted on file 03")
	}
}

// Anything that selects ends the session, which is what a poll does.
func TestNTAG424SecureSelectAndVersionEndTheSession(t *testing.T) {
	c := NTAG424(secureUID)

	mustAuth(t, c, 0, zeroKey)
	send(t, c, nfc.SelectFileByAIDAPDU(type4NDEFAppAID))
	if c.NTAG424Authenticated() {
		t.Error("session survived an ISO SELECT")
	}

	mustAuth(t, c, 0, zeroKey)
	send(t, c, nfc.SelectFileAPDU([]byte{0xE1, 0x04}))
	if c.NTAG424Authenticated() {
		t.Error("session survived a refused ISO SELECT")
	}

	mustAuth(t, c, 0, zeroKey)
	send(t, c, nfc.NTAG424GetVersionAPDU())
	if c.NTAG424Authenticated() {
		t.Error("session survived a wrapped GET_VERSION")
	}

	s := mustAuth(t, c, 0, zeroKey)
	cmd, _ := ntag424.GetKeyVersion(s, 0)
	send(t, c, cmd)
	if !c.NTAG424Authenticated() {
		t.Error("an ordinary command ended the session")
	}

	if _, err := c.Tag().ReadData(); err != nil && !nfc.IsNoPayloadError(err) {
		t.Fatalf("ReadData: %v", err)
	}
	if c.NTAG424Authenticated() {
		t.Error("session survived the driver's ReadData")
	}

	cmd, _ = ntag424.GetKeyVersion(s, 0)
	if _, err := ntag424.ParseKeyVersion(s, send(t, c, cmd)); !errors.Is(err, ntag424.ErrSessionLost) {
		t.Errorf("command after the session ended: %v, want ErrSessionLost", err)
	}
}

func TestNTAG424SecureBadMACEndsTheSession(t *testing.T) {
	c := NTAG424(secureUID)
	s := mustAuth(t, c, 0, zeroKey)
	cmd, _ := ntag424.GetKeyVersion(s, 0)
	cmd[len(cmd)-2] ^= 0xFF
	if sw := swOf(send(t, c, cmd), nil); sw != 0x911E {
		t.Errorf("SW = %04X, want 911E", sw)
	}
	if c.NTAG424Authenticated() {
		t.Error("session survived a bad MAC")
	}
}

func TestNTAG424SecureRandomID(t *testing.T) {
	c := NTAG424(secureUID, NTAG424WithRandomID())
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		uid := c.NTAG424NewPresentation()
		if len(uid) != 4 || uid[0] != 0x08 {
			t.Fatalf("presented UID = %X, want 4 bytes opening 08", uid)
		}
		seen[nfc.BytesToHex(uid)] = true
	}
	if len(seen) < 2 {
		t.Error("the presented UID never changed")
	}

	s := mustAuth(t, c, 0, zeroKey)
	cmd, _ := ntag424.GetCardUID(s)
	uid, err := ntag424.ParseCardUID(s, send(t, c, cmd))
	if err != nil || nfc.BytesToHex(uid) != secureUID {
		t.Errorf("GetCardUID = %X, %v; want the real UID", uid, err)
	}

	plain := NTAG424(secureUID)
	if got := plain.NTAG424PresentedUID(); nfc.BytesToHex(got) != secureUID {
		t.Errorf("presented UID without random ID = %X", got)
	}
}

func sdmKeys() map[byte][]byte {
	return map[byte][]byte{
		1: bytes.Repeat([]byte{0x11}, 16),
		2: bytes.Repeat([]byte{0x22}, 16),
	}
}

func tapURL(t *testing.T, c *EmulatedCard) string {
	t.Helper()
	data, err := c.Tag().ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	msg, err := nfc.DecodeNDEF(data)
	if err != nil {
		t.Fatal(err)
	}
	u, err := msg.GetURI()
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNTAG424SecureSDMTapVerifies(t *testing.T) {
	for _, tc := range []struct {
		name, tmpl string
		opts       ntag424.SDMOptions
	}{
		{"picc", "https://davi.example/t?picc={picc}&cmac={mac}",
			ntag424.SDMOptions{MetaRead: 1, FileRead: 2, CounterRet: 0, Change: 0, Read: 0xE, Write: 0, ReadWrite: 0}},
		{"plain", "https://davi.example/t?uid={uid}&ctr={ctr}&cmac={mac}",
			ntag424.SDMOptions{FileRead: 2, CounterRet: 0xE, Change: 0, Read: 0xE, Write: 0xE, ReadWrite: 0xE}},
		{"enc", "https://davi.example/t?picc={picc}&enc={enc}&cmac={mac}",
			ntag424.SDMOptions{MetaRead: 1, FileRead: 2, CounterRet: 0, Change: 0, Read: 0xE, Write: 0, ReadWrite: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ntag424.PlanSDM(tc.tmpl, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			c := NTAG424(secureUID, NTAG424WithKeys(sdmKeys()), NTAG424WithSDM(plan))
			keys := ntag424.Keys{MetaRead: sdmKeys()[1], FileRead: sdmKeys()[2]}

			var last uint32
			for i := 0; i < 3; i++ {
				tap, err := ntag424.VerifyURL(tapURL(t, c), keys)
				if err != nil {
					t.Fatalf("tap %d: %v", i, err)
				}
				if tap.UIDString() != secureUID {
					t.Errorf("UID = %s", tap.UIDString())
				}
				if i > 0 && tap.ReadCounter != last+1 {
					t.Errorf("counter = %d after %d, want one more", tap.ReadCounter, last)
				}
				last = tap.ReadCounter
				if tc.name == "enc" && !bytes.Equal(tap.FileData, bytes.Repeat([]byte("0"), 16)) {
					t.Errorf("file data = %q", tap.FileData)
				}
			}
			if got := c.NTAG424ReadCounter(); got != 3 {
				t.Errorf("read counter = %d, want 3", got)
			}
		})
	}
}

// The counter moves once per selection however many reads follow, and a wrong
// key does not verify.
func TestNTAG424SecureSDMCounterPerSelection(t *testing.T) {
	plan, err := ntag424.PlanSDM("https://davi.example/t?picc={picc}&cmac={mac}",
		ntag424.SDMOptions{MetaRead: 1, FileRead: 2, Change: 0, Read: 0xE, Write: 0, ReadWrite: 0})
	if err != nil {
		t.Fatal(err)
	}
	c := NTAG424(secureUID, NTAG424WithKeys(sdmKeys()), NTAG424WithSDM(plan))

	send(t, c, nfc.SelectFileByAIDAPDU(type4NDEFAppAID))
	send(t, c, nfc.SelectFileAPDU([]byte{0xE1, 0x04}))
	a := send(t, c, nfc.ReadBinaryExtAPDU(0, 100))
	b := send(t, c, nfc.ReadBinaryExtAPDU(0, 100))
	if !bytes.Equal(a, b) {
		t.Error("two reads in one selection differ")
	}
	if got := c.NTAG424ReadCounter(); got != 1 {
		t.Errorf("counter = %d, want 1", got)
	}

	if _, err := ntag424.VerifyURL(tapURL(t, c), ntag424.Keys{MetaRead: zeroKey, FileRead: zeroKey}); err == nil {
		t.Error("a tap verified under the wrong keys")
	}
}

// Enabling SDM over the wire, then tapping, is the provisioning path.
func TestNTAG424SecureProvisionSDMOverTheWire(t *testing.T) {
	plan, err := ntag424.PlanSDM("https://davi.example/t?uid={uid}&ctr={ctr}&cmac={mac}",
		ntag424.SDMOptions{FileRead: 2, CounterRet: 0, Change: 0, Read: 0xE, Write: 0xE, ReadWrite: 0xE})
	if err != nil {
		t.Fatal(err)
	}
	c := NTAG424(secureUID, NTAG424WithKeys(map[byte][]byte{2: sdmKeys()[2]}))
	if err := c.Tag().WriteData(plan.NDEF[2:]); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	s := mustAuth(t, c, 0, zeroKey)
	enc, _ := plan.Settings.Encode()
	cmd, _ := ntag424.ChangeFileSettings(s, ntag424.NDEFFileNo, enc)
	if _, err := ntag424.CheckResponse(s, send(t, c, cmd), ntag424.CommFull); err != nil {
		t.Fatalf("ChangeFileSettings: %v", err)
	}
	cmd, _ = ntag424.GetFileSettings(s, ntag424.NDEFFileNo)
	got, err := ntag424.ParseFileSettingsResponse(s, send(t, c, cmd))
	if err != nil || !got.SDMEnabled || got.MACOffset != plan.Settings.MACOffset {
		t.Fatalf("read back = %+v, %v", got, err)
	}

	keys := ntag424.Keys{FileRead: sdmKeys()[2]}
	for i := 0; i < 2; i++ {
		if _, err := ntag424.VerifyURL(tapURL(t, c), keys); err != nil {
			t.Fatalf("tap %d: %v", i, err)
		}
	}

	s = mustAuth(t, c, 0, zeroKey)
	cmd, _ = ntag424.GetFileCounters(s, ntag424.NDEFFileNo)
	n, err := ntag424.ParseFileCounters(s, send(t, c, cmd))
	if err != nil || n != 2 {
		t.Errorf("GetFileCounters = %d, %v; want 2", n, err)
	}
}

func TestNTAG424SecureReplayGuardSeesCounter(t *testing.T) {
	plan, _ := ntag424.PlanSDM("https://davi.example/t?picc={picc}&cmac={mac}",
		ntag424.SDMOptions{MetaRead: 1, FileRead: 2, Change: 0, Read: 0xE, Write: 0, ReadWrite: 0})
	c := NTAG424(secureUID, NTAG424WithKeys(sdmKeys()), NTAG424WithSDM(plan))
	keys := ntag424.Keys{MetaRead: sdmKeys()[1], FileRead: sdmKeys()[2]}
	store := &ntag424.MemoryCounterStore{}

	first := tapURL(t, c)
	if _, err := ntag424.VerifyURLFresh(first, keys, store); err != nil {
		t.Fatal(err)
	}
	if _, err := ntag424.VerifyURLFresh(first, keys, store); !errors.Is(err, ntag424.ErrReplay) {
		t.Errorf("replay: %v", err)
	}
	if _, err := ntag424.VerifyURLFresh(tapURL(t, c), keys, store); err != nil {
		t.Errorf("next tap: %v", err)
	}
}
