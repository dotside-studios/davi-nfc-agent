package keyfile

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// Keys made for these tests. They are not keys from any card or document.
var (
	testMaster = bytes.Repeat([]byte{0xA1}, 16)
	testSlot1  = bytes.Repeat([]byte{0xB2}, 16)
	testSlot2  = bytes.Repeat([]byte{0xC3}, 16)
	testDF     = bytes.Repeat([]byte{0xD4}, 16)
	testClassA = []byte{0xE5, 0xE5, 0xE5, 0xE5, 0xE5, 0xE5}
	testClassB = []byte{0xF6, 0xF6, 0xF6, 0xF6, 0xF6, 0xF6}
)

func h(b []byte) string { return hex.EncodeToString(b) }

func fullFile() string {
	return fmt.Sprintf(`{
  "ntag424": {
    "master": %q,
    "diversify": true,
    "systemID": "4E4643",
    "slots": {"1": %q, "2": %q},
    "allowLRP": true
  },
  "classic": [%q, %q],
  "desfire": {"slots": {"0": %q, "3": %q}}
}`, h(testMaster), h(testSlot1), h(testSlot2), h(testClassA), h(testClassB), h(testDF), h(testDF))
}

func TestParseFullFile(t *testing.T) {
	keys, err := Parse([]byte(fullFile()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	n := keys.NTAG424
	if !bytes.Equal(n.Master, testMaster) || !n.Diversify || !n.AllowLRP || h(n.SystemID) != "4e4643" {
		t.Errorf("NTAG424 = %+v", n)
	}
	if !bytes.Equal(n.Slots[1], testSlot1) || !bytes.Equal(n.Slots[2], testSlot2) || len(n.Slots) != 2 {
		t.Errorf("NTAG424 slots = %d entries", len(n.Slots))
	}
	if len(keys.Classic) != 2 || !bytes.Equal(keys.Classic[0], testClassA) || !bytes.Equal(keys.Classic[1], testClassB) {
		t.Errorf("Classic = %d keys", len(keys.Classic))
	}
	if len(keys.DESFire) != 2 || !bytes.Equal(keys.DESFire[3], testDF) {
		t.Errorf("DESFire = %d keys", len(keys.DESFire))
	}
	want := "MIFARE Classic keys: 2; DESFire keys: slots 0,3; NTAG 424 keys: master (diversified), slots 1,2, LRP allowed"
	if got := keys.Summary(); got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestParseAcceptsUppercaseHexAndPartialFiles(t *testing.T) {
	keys, err := Parse([]byte(`{"classic": ["FFFFFFFFFFFF"]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(keys.Classic) != 1 || !keys.NTAG424.Empty() || len(keys.DESFire) != 0 {
		t.Errorf("keys = %+v", keys)
	}
	if empty, err := Parse([]byte(`{}`)); err != nil || !empty.Empty() {
		t.Errorf("empty document = %+v, %v", empty, err)
	}
}

func TestParseRefusals(t *testing.T) {
	k16 := h(testMaster)
	cases := []struct {
		name, doc, want string
	}{
		{"unknown top-level field", `{"ntag": {}}`, `unknown field "ntag"`},
		{"unknown ntag424 field", `{"ntag424": {"masterr": "` + k16 + `"}}`, `unknown field "masterr"`},
		{"unknown desfire field", `{"desfire": {"keys": {}}}`, `unknown field "keys"`},
		{"not JSON", `master=1`, "not valid JSON"},
		{"empty", ``, "empty or truncated"},
		{"trailing data", `{} {}`, "after the JSON document"},
		{"wrong type", `{"classic": "abc"}`, "must be a JSON"},
		{"bad hex ntag424 master", `{"ntag424": {"master": "` + strings.Repeat("zz", 16) + `"}}`, "ntag424.master: not valid hex"},
		{"short ntag424 master", `{"ntag424": {"master": "` + k16[:30] + `"}}`, "ntag424.master: must be 32 hex characters"},
		{"long ntag424 slot", `{"ntag424": {"slots": {"0": "` + k16 + `00"}}}`, "ntag424.slots: slot 0: must be 32 hex characters"},
		{"ntag424 slot out of range", `{"ntag424": {"slots": {"5": "` + k16 + `"}}}`, "slot names must be decimal key numbers 0 to 4"},
		{"ntag424 slot not a number", `{"ntag424": {"slots": {"one": "` + k16 + `"}}}`, "slot names must be decimal"},
		{"ntag424 slot padded", `{"ntag424": {"slots": {"01": "` + k16 + `"}}}`, "slot names must be decimal"},
		{"desfire slot out of range", `{"desfire": {"slots": {"14": "` + k16 + `"}}}`, "key numbers 0 to 13"},
		{"desfire short key", `{"desfire": {"slots": {"1": "00"}}}`, "desfire.slots: slot 1: must be 32 hex characters"},
		{"classic short key", `{"classic": ["` + k16[:10] + `"]}`, "classic[0]: must be 12 hex characters"},
		{"classic key is an AES key", `{"classic": ["` + k16 + `"]}`, "classic[0]: must be 12 hex characters"},
		{"bad hex classic", `{"classic": ["12345678901g"]}`, "classic[0]: not valid hex"},
		{"0x prefix", `{"classic": ["0xFFFFFFFFFF"]}`, "classic[0]"},
		{"diversify without master", `{"ntag424": {"diversify": true}}`, "diversify needs"},
		{"systemID without diversify", `{"ntag424": {"master": "` + k16 + `", "systemID": "01"}}`, "only used with"},
		{"bad systemID hex", `{"ntag424": {"master": "` + k16 + `", "diversify": true, "systemID": "xyz"}}`, "systemID: not valid hex"},
		{"long systemID", `{"ntag424": {"master": "` + k16 + `", "diversify": true, "systemID": "` + strings.Repeat("01", 25) + `"}}`, "systemID: 25 bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.doc))
			if err == nil {
				t.Fatal("Parse accepted it")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// An error says where the problem is, never what the key was.
func TestErrorsNeverEchoKeyMaterial(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	docs := []string{
		`{"ntag424": {"master": "` + secret + `zz"}}`,
		`{"ntag424": {"master": "` + secret[:30] + `"}}`,
		`{"ntag424": {"slots": {"0": "` + secret + `ff"}}}`,
		`{"ntag424": {"slots": {"` + secret + `": "` + secret + `"}}}`,
		`{"` + secret + `": "x"}`,
		`{"ntag424": {"` + secret + `": "x"}}`,
		`{"classic": ["` + secret[:12] + `00"]}`,
		`{"classic": [` + secret[:12] + `]}`,
		`{"classic": {"` + secret + `": 1}}`,
		`{"ntag424": {"master": ` + secret + `}}`,
		`{"ntag424": {"master": {"` + secret + `": 1}}}`,
		`{"desfire": {"slots": {"1": 12345678}}}`,
		`{"desfire": {"slots": {"` + secret + `": 1}}}`,
		`{"ntag424": {"diversify": "` + secret + `"}}`,
		`{"ntag424": {"slots": {"0": "` + secret + `"}}} ` + secret,
		`{"ntag424": {"systemID": "` + secret + `zz"}}`,
		`{"ntag424": {"master": "` + secret + `", "diversify": true, "systemID": "` + secret + secret + `"}}`,
	}
	for _, doc := range docs {
		_, err := Parse([]byte(doc))
		if err == nil {
			t.Errorf("Parse accepted %q", doc)
			continue
		}
		for _, frag := range []string{secret, secret[:12], "12345678"} {
			if strings.Contains(err.Error(), frag) {
				t.Errorf("error %q echoes key material %q (document %q)", err, frag, doc)
			}
		}
	}
}

func writeKeyFile(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	keys, err := Load(writeKeyFile(t, 0o600, fullFile()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if keys.Empty() || len(keys.Classic) != 2 {
		t.Errorf("keys = %+v", keys)
	}

	if _, err := Load(writeKeyFile(t, 0o400, fullFile())); err != nil {
		t.Errorf("a read-only owner file was refused: %v", err)
	}
}

func TestLoadRefusesAFileOthersCanAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits to check; access is an ACL")
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666, 0o601} {
		path := writeKeyFile(t, mode, fullFile())
		_, err := Load(path)
		if err == nil {
			t.Errorf("mode %04o: Load accepted the file", mode)
			continue
		}
		if !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %04o: error %q does not tell the operator to chmod 600", mode, err)
		}
		if strings.Contains(err.Error(), h(testMaster)) {
			t.Errorf("mode %04o: error echoes a key", mode)
		}
	}
}

func TestLoadRefusesWhatIsNotARegularFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory: err = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Error("a symlink to a directory was accepted")
	}
	if _, err := Load("/dev/null"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("/dev/null: err = %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatal("Load accepted a missing file")
	}
}

func TestLoadRefusesAnOversizedFile(t *testing.T) {
	big := `{"classic": []}` + strings.Repeat(" ", maxFileSize)
	if _, err := Load(writeKeyFile(t, 0o600, big)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
}

func TestSummaryHoldsNoKeyMaterial(t *testing.T) {
	keys, err := Parse([]byte(fullFile()))
	if err != nil {
		t.Fatal(err)
	}
	s := keys.Summary()
	for _, k := range [][]byte{testMaster, testSlot1, testSlot2, testDF, testClassA, testClassB} {
		if strings.Contains(s, h(k)) || strings.Contains(s, h(k)[:6]) {
			t.Errorf("summary %q holds key material", s)
		}
	}
	if (Keys{}).Summary() != "no keys" {
		t.Errorf("empty summary = %q", (Keys{}).Summary())
	}
}

func TestCopySharesNoMemory(t *testing.T) {
	keys, err := Parse([]byte(fullFile()))
	if err != nil {
		t.Fatal(err)
	}
	cp := keys.Copy()
	cp.Classic[0][0] ^= 0xFF
	cp.DESFire[0][0] ^= 0xFF
	cp.NTAG424.Master[0] ^= 0xFF
	cp.NTAG424.Slots[1][0] ^= 0xFF
	if !bytes.Equal(keys.Classic[0], testClassA) || !bytes.Equal(keys.DESFire[0], testDF) ||
		!bytes.Equal(keys.NTAG424.Master, testMaster) || !bytes.Equal(keys.NTAG424.Slots[1], testSlot1) {
		t.Error("mutating a copy changed the original")
	}
}

const lane = "lane1"

// These authenticate an emulated NTAG 424 DNA with keys that went through a
// key file on disk and Apply, which is the path the shipped command takes. The
// emulator and the driver are the repository's; no card is involved.

func readSigOn(t *testing.T, lanes *nfctest.EmulatedLanes, uid string) error {
	t.Helper()
	return lanes.WithNTAG424Tag(context.Background(), lane, uid, func(op nfc.NTAG424Operator) error {
		_, err := op.ReadSig()
		return err
	})
}

func presented(t *testing.T, card *nfctest.EmulatedCard) *nfctest.EmulatedLanes {
	t.Helper()
	lanes := nfctest.NewEmulatedLanes(t, lane)
	lanes.Present(lane, card)
	awaitCard(t, lanes)
	return lanes
}

func awaitCard(t *testing.T, lanes *nfctest.EmulatedLanes) {
	t.Helper()
	for range 300 {
		if _, _, ok := lanes.TagOn(lane); ok {
			return
		}
		sleepBriefly()
	}
	t.Fatal("the reader never reported the card")
}

func TestKeysFromAFileAuthenticateToAnNTAG424(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	doc := fmt.Sprintf(`{"ntag424": {"slots": {"0": %q}}}`, h(testSlot1))
	keys, err := Load(writeKeyFile(t, 0o600, doc))
	if err != nil {
		t.Fatal(err)
	}

	lanes := presented(t, nfctest.NTAG424(uid, nfctest.NTAG424WithKeys(map[byte][]byte{0: testSlot1})))

	err = readSigOn(t, lanes, uid)
	if err == nil {
		t.Fatal("the operation worked before any key was loaded")
	}
	if !strings.Contains(err.Error(), "-keys") {
		t.Errorf("the refusal %q does not say how to load keys", err)
	}

	keys.Apply(lanes.Supervisor)
	if err := readSigOn(t, lanes, uid); err != nil {
		t.Errorf("with the file's key loaded: %v", err)
	}
}

func TestADiversifiedMasterFromAFileAuthenticates(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	set := ntag424.KeySet{Master: testMaster, Diversify: true, SystemID: []byte{0x4E, 0x46, 0x43}}
	doc := fmt.Sprintf(`{"ntag424": {"master": %q, "diversify": true, "systemID": "4E4643"}}`, h(testMaster))
	keys, err := Load(writeKeyFile(t, 0o600, doc))
	if err != nil {
		t.Fatal(err)
	}

	lanes := presented(t, nfctest.NTAG424(uid, nfctest.NTAG424WithKeySet(set)))
	keys.Apply(lanes.Supervisor)
	if err := readSigOn(t, lanes, uid); err != nil {
		t.Errorf("with the diversified master loaded: %v", err)
	}
}

func TestAWrongKeyFromAFileIsRefused(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	doc := fmt.Sprintf(`{"ntag424": {"slots": {"0": %q}}}`, h(testSlot2))
	keys, err := Load(writeKeyFile(t, 0o600, doc))
	if err != nil {
		t.Fatal(err)
	}

	lanes := presented(t, nfctest.NTAG424(uid, nfctest.NTAG424WithKeys(map[byte][]byte{0: testSlot1})))
	keys.Apply(lanes.Supervisor)
	err = readSigOn(t, lanes, uid)
	if err == nil {
		t.Fatal("the wrong key authenticated")
	}
	for _, k := range [][]byte{testSlot1, testSlot2} {
		if strings.Contains(err.Error(), h(k)) {
			t.Errorf("error %q holds key material", err)
		}
	}
}

func TestRotatingKeysThroughApplyReplacesThem(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	old, err := Parse([]byte(fmt.Sprintf(`{"ntag424": {"slots": {"0": %q}}}`, h(testSlot1))))
	if err != nil {
		t.Fatal(err)
	}
	next, err := Parse([]byte(fmt.Sprintf(`{"ntag424": {"slots": {"0": %q}}}`, h(testSlot2))))
	if err != nil {
		t.Fatal(err)
	}

	lanes := presented(t, nfctest.NTAG424(uid, nfctest.NTAG424WithKeys(map[byte][]byte{0: testSlot2})))
	old.Apply(lanes.Supervisor)
	if err := readSigOn(t, lanes, uid); err == nil {
		t.Fatal("the old key authenticated a card holding the new one")
	}
	next.Apply(lanes.Supervisor)
	if err := readSigOn(t, lanes, uid); err != nil {
		t.Errorf("after rotating to the new key: %v", err)
	}
}

func sleepBriefly() { time.Sleep(10 * time.Millisecond) }
