//go:build hardware

package hwtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/hwtest/scenario"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/keyfile"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
)

// The environment the tests read. See README.md.
const (
	envReader       = "DAVI_HW_READER"
	envOut          = "DAVI_HW_OUT"
	envKeys         = "DAVI_HW_KEYS"
	envEnableLRPUID = "DAVI_HW_ENABLE_LRP_UID"
	envIncludeKeys  = "DAVI_HW_INCLUDE_KEYS"
	envKeepSDM      = "DAVI_HW_KEEP_SDM"

	tagWaitTimeout   = 30 * time.Second
	supervisorBudget = 10 * time.Second
)

// hardware is a started supervisor over the PC/SC readers, with the recorder
// attached and the tag on the chosen reader scanned.
type hardware struct {
	sup *nfc.Supervisor
	rec *fixture.Recorder
	tag scenario.Tag
}

// openHardware picks the reader, starts the supervisor under the recorder and
// waits for a tag. It installs the PC/SC observer before the supervisor starts,
// which is when the manager opens the readers.
func openHardware(t *testing.T) *hardware {
	t.Helper()

	m := pcsc.NewManager()
	t.Cleanup(m.Close)

	listings, err := m.Devices()
	if err != nil {
		t.Fatalf("listing PC/SC readers: %v", err)
	}
	if len(listings) == 0 {
		t.Fatalf("no PC/SC reader is attached (is pcscd running?)")
	}
	names := make([]string, len(listings))
	for i, l := range listings {
		names[i] = l.ID
	}
	reader, err := chooseReader(names, os.Getenv(envReader))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("using reader %q (of %q)", reader, names)

	rec := &fixture.Recorder{}
	rec.Only(reader)
	pcsc.SetObserver(pcsc.Observer{Connected: rec.Connected, Transmit: rec.Transmit})
	t.Cleanup(func() { pcsc.SetObserver(pcsc.Observer{}) })

	sup, err := nfc.NewSupervisor(m, supervisorBudget)
	if err != nil {
		t.Fatal(err)
	}
	watch := scenario.Watch(sup)
	if err := sup.Start(); err != nil {
		t.Fatalf("starting the supervisor: %v", err)
	}
	t.Cleanup(func() { sup.Stop(); watch.Close() })

	t.Logf("place one tag on %q (waiting up to %s)", reader, tagWaitTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), tagWaitTimeout)
	defer cancel()
	tag, err := watch.Wait(ctx, reader)
	if err != nil {
		t.Fatalf("%v. Is a tag on the reader, and is another program holding it?", err)
	}
	t.Logf("tag %s (%s)", tag.UID, tag.Card.Type)
	return &hardware{sup: sup, rec: rec, tag: tag}
}

// chooseReader returns the first reader whose name contains want, ignoring
// case, or the first reader when want is empty.
func chooseReader(names []string, want string) (string, error) {
	if want == "" {
		return names[0], nil
	}
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), strings.ToLower(want)) {
			return n, nil
		}
	}
	return "", fmt.Errorf("%s=%q matches none of the readers %q", envReader, want, names)
}

// outDir is where fixtures are written: DAVI_HW_OUT, or hwtest-out at the
// repository root. A relative DAVI_HW_OUT is taken from the repository root too,
// because go test runs in the package directory.
func outDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(envOut)
	switch {
	case dir == "":
		return filepath.Join(repoRoot(t), "hwtest-out")
	case filepath.IsAbs(dir):
		return dir
	default:
		return filepath.Join(repoRoot(t), dir)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above the working directory")
		}
		dir = parent
	}
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func fileSafe(s string) string {
	return strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// save writes a fixture and says where, so a run that failed still leaves its
// capture behind.
func save(t *testing.T, name string, v any) {
	t.Helper()
	path, err := fixture.Write(outDir(t), name, v)
	if err != nil {
		t.Errorf("writing the fixture: %v", err)
		return
	}
	t.Logf("FIXTURE WRITTEN: %s", path)
}

// loadKeys reads DAVI_HW_KEYS with the key file loader the agent uses,
// permission checks included.
func loadKeys(t *testing.T) keyfile.Keys {
	t.Helper()
	path := os.Getenv(envKeys)
	if path == "" {
		t.Fatalf("%s is not set. It names a key file in the format of docs/card-keys.md holding the tag's NTAG 424 keys", envKeys)
	}
	keys, err := keyfile.Load(path)
	if err != nil {
		t.Fatalf("%s: %v", envKeys, err)
	}
	if keys.NTAG424.Empty() {
		t.Fatalf("%s holds no ntag424 keys", envKeys)
	}
	t.Logf("keys: %s", keys.Summary())
	return keys
}

func envOn(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
