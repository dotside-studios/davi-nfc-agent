package agent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/keyfile"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
)

// Keys made for these tests; they are not from any card or document.
var (
	agentKey0 = bytes.Repeat([]byte{0x5A}, 16)
	agentKey1 = bytes.Repeat([]byte{0x6B}, 16)
)

const agentClassicKey = "a1b2c3d4e5f6"

func writeAgentKeyFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func agentKeyDoc() string {
	return fmt.Sprintf(`{"ntag424": {"slots": {"0": %q, "1": %q}}, "classic": [%q]}`,
		hex.EncodeToString(agentKey0), hex.EncodeToString(agentKey1), agentClassicKey)
}

func captureProcessLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	before := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(before) })
	return &buf
}

func TestSetupLoadsTheKeyFile(t *testing.T) {
	out := captureProcessLog(t)
	ring := logbuf.New(logbuf.DefaultCapacity)
	logbuf.Install(ring)
	t.Cleanup(func() { logbuf.Install(nil) })

	opts := testOptions(t)
	opts.KeysFile = writeAgentKeyFile(t, agentKeyDoc())
	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if !strings.Contains(out.String(), "NTAG 424 keys: slots 0,1") {
		t.Errorf("the log does not say which keys were loaded:\n%s", out)
	}
	logged := out.String()
	for _, e := range ring.Entries() {
		logged += e.Message
	}
	for _, frag := range []string{
		hex.EncodeToString(agentKey0), hex.EncodeToString(agentKey1),
		agentClassicKey, strings.ToUpper(agentClassicKey),
		hex.EncodeToString(agentKey0)[:8],
	} {
		if strings.Contains(logged, frag) {
			t.Errorf("the log holds key material %q", frag)
		}
	}
	if len(rt.Agent.keysSnapshot().NTAG424.Slots) != 2 {
		t.Error("the agent does not hold the keys")
	}
}

func TestSetupReadsKeysFromTheEnvironment(t *testing.T) {
	t.Setenv("DAVI_NFC_KEYS", writeAgentKeyFile(t, agentKeyDoc()))
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(rt.Agent.keysSnapshot().NTAG424.Slots) != 2 {
		t.Error("DAVI_NFC_KEYS was not read")
	}
}

func TestTheFlagBeatsTheEnvironment(t *testing.T) {
	t.Setenv("DAVI_NFC_KEYS", filepath.Join(t.TempDir(), "absent.json"))
	opts := testOptions(t)
	opts.KeysFile = writeAgentKeyFile(t, agentKeyDoc())
	if _, err := Setup(opts, nfc.NewMockManager()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

func TestSetupFailsOnAKeyFileItCannotLoad(t *testing.T) {
	cases := map[string]string{
		"missing":  filepath.Join(t.TempDir(), "absent.json"),
		"badKey":   writeAgentKeyFile(t, `{"classic": ["`+hex.EncodeToString(agentKey0)+`"]}`),
		"unknown":  writeAgentKeyFile(t, `{"nope": 1}`),
		"empty":    writeAgentKeyFile(t, ``),
		"notAFile": t.TempDir(),
	}
	if runtime.GOOS != "windows" {
		open := writeAgentKeyFile(t, agentKeyDoc())
		if err := os.Chmod(open, 0o644); err != nil {
			t.Fatal(err)
		}
		cases["tooOpen"] = open
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			opts := testOptions(t)
			opts.KeysFile = path
			_, err := Setup(opts, nfc.NewMockManager())
			if err == nil {
				t.Fatal("Setup ran without the keys it was told to load")
			}
			if strings.Contains(err.Error(), hex.EncodeToString(agentKey0)) {
				t.Errorf("error %q holds key material", err)
			}
		})
	}
}

func TestPreferencesCarryNoKeys(t *testing.T) {
	opts := testOptions(t)
	opts.KeysFile = writeAgentKeyFile(t, agentKeyDoc())
	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rt.Agent.Preferences())
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.ToLower(string(data)); strings.Contains(s, "key") || strings.Contains(s, hex.EncodeToString(agentKey0)) {
		t.Errorf("preferences mention keys: %s", data)
	}
}

// readSigOnTheCard waits for the agent's reader to hold the card, then runs an
// operation that needs a session on it.
func readSigOnTheCard(t *testing.T, a *Agent, uid string) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := a.Supervisor(); s != nil {
			for _, d := range s.Devices() {
				if _, _, ok := s.TagOn(d); ok {
					return s.WithNTAG424Tag(context.Background(), d, uid, func(op nfc.NTAG424Operator) error {
						_, err := op.ReadSig()
						return err
					})
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the agent never saw the card")
	return nil
}

func startedOverAnNTAG424(t *testing.T, opts *Options, uid string) *Runtime {
	t.Helper()
	m := nfc.NewMockManager()
	card := nfctest.NTAG424(uid, nfctest.NTAG424WithKeys(map[byte][]byte{0: agentKey0, 1: agentKey1}))
	m.MockDevice.SetTags([]nfc.Tag{card.Tag()})

	rt, err := Setup(opts, m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(rt.Agent.Shutdown)
	return rt
}

// A real agent, started over an emulated NTAG 424 DNA, authenticates with the
// keys it was given through the key file and refuses without them.
func TestAgentAuthenticatesWithKeysFromTheKeyFile(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"

	t.Run("with keys", func(t *testing.T) {
		opts := testOptions(t)
		opts.KeysFile = writeAgentKeyFile(t, agentKeyDoc())
		rt := startedOverAnNTAG424(t, opts, uid)
		if err := readSigOnTheCard(t, rt.Agent, uid); err != nil {
			t.Errorf("ReadSig with the file's keys: %v", err)
		}
	})

	t.Run("without keys", func(t *testing.T) {
		rt := startedOverAnNTAG424(t, testOptions(t), uid)
		err := readSigOnTheCard(t, rt.Agent, uid)
		if err == nil {
			t.Fatal("ReadSig worked with no keys")
		}
		if !strings.Contains(err.Error(), "-keys") {
			t.Errorf("the refusal %q does not say keys are loaded with -keys", err)
		}
	})
}

func TestSetKeysReachesARunningAgent(t *testing.T) {
	const uid = "04A1B2C3D4E5F6"
	rt := startedOverAnNTAG424(t, testOptions(t), uid)
	if err := readSigOnTheCard(t, rt.Agent, uid); err == nil {
		t.Fatal("ReadSig worked with no keys")
	}

	keys, err := keyfile.Parse([]byte(agentKeyDoc()))
	if err != nil {
		t.Fatal(err)
	}
	rt.Agent.SetKeys(keys)

	if err := readSigOnTheCard(t, rt.Agent, uid); err != nil {
		t.Errorf("ReadSig after SetKeys: %v", err)
	}
}
