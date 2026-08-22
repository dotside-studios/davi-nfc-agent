package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// An agent that has never had a preference changed should not acquire a
// settings file merely by starting, so that "no file" keeps meaning
// "nothing was ever configured here".
func TestNewDoesNotWriteOnLoad(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Errorf("settings file created on load; want none (err=%v)", err)
	}
	if got := store.Get().Mode; got != ModeReadWrite {
		t.Errorf("default mode = %q, want %q", got, ModeReadWrite)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cardType := nfc.GetAllCardTypes()[0]
	if err := store.Save(Settings{
		Mode:                ModeReadOnly,
		CardTypes:           []string{cardType},
		DevicePath:          "ACS ACR122U",
		Port:                9480,
		RequirePairedDevice: true,
		ReaderFeedback:      true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	got := reloaded.Get()
	if got.Mode != ModeReadOnly {
		t.Errorf("mode = %q, want %q", got.Mode, ModeReadOnly)
	}
	if len(got.CardTypes) != 1 || got.CardTypes[0] != cardType {
		t.Errorf("cardTypes = %v, want [%s]", got.CardTypes, cardType)
	}
	if got.DevicePath != "ACS ACR122U" {
		t.Errorf("devicePath = %q", got.DevicePath)
	}
	if got.Port != 9480 {
		t.Errorf("port = %d, want 9480", got.Port)
	}
	if !got.RequirePairedDevice {
		t.Error("requirePairedDevice = false, want true")
	}
	if !got.ReaderFeedback {
		t.Error("readerFeedback = false, want true")
	}
}

// A file written by an older build lacks the newer keys. They must land on
// their defaults, not on the zero value.
func TestPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(`{"devicePath":"reader-1"}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := store.Get()
	if got.DevicePath != "reader-1" {
		t.Errorf("devicePath = %q, want %q", got.DevicePath, "reader-1")
	}
	if got.Mode != ModeReadWrite {
		t.Errorf("mode = %q, want the default %q", got.Mode, ModeReadWrite)
	}
	if got.ReaderFeedback {
		t.Error("readerFeedback = true, want the default false")
	}
}

func TestNormalizeRejectsUnknownValues(t *testing.T) {
	got := Normalize(Settings{
		Mode:      "sideways",
		CardTypes: []string{"NotARealCardType"},
		Port:      70000,
	})

	if got.Mode != ModeReadWrite {
		t.Errorf("unknown mode = %q, want fallback %q", got.Mode, ModeReadWrite)
	}
	if len(got.CardTypes) != 0 {
		t.Errorf("unknown card type survived: %v", got.CardTypes)
	}
	if got.Port != 0 {
		t.Errorf("out-of-range port = %d, want 0", got.Port)
	}
}

func TestNormalizeDeduplicatesAndSorts(t *testing.T) {
	all := nfc.GetAllCardTypes()
	if len(all) < 2 {
		t.Skip("build supports fewer than two card types")
	}

	got := Normalize(Settings{
		Mode:      ModeReadWrite,
		CardTypes: []string{all[1], all[0], all[1]},
	}).CardTypes

	if len(got) != 2 {
		t.Fatalf("cardTypes = %v, want 2 entries", got)
	}
	if got[0] > got[1] {
		t.Errorf("cardTypes not sorted: %v", got)
	}
}

// "every supported type" and "no filter" are the same policy. Collapsing them
// keeps unfiltered a single representable state.
func TestNormalizeCollapsesFullFilterToNone(t *testing.T) {
	got := Normalize(Settings{
		Mode:      ModeReadWrite,
		CardTypes: nfc.GetAllCardTypes(),
	})
	if len(got.CardTypes) != 0 {
		t.Errorf("full filter kept as %v, want none", got.CardTypes)
	}
}

func TestUpdateAppliesMutationAndPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var notified Settings
	store.OnChange(func(s Settings) { notified = s })

	got, err := store.Update(func(s *Settings) { s.Mode = ModeWriteOnly })
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Mode != ModeWriteOnly {
		t.Errorf("returned mode = %q", got.Mode)
	}
	if notified.Mode != ModeWriteOnly {
		t.Errorf("OnChange saw mode %q", notified.Mode)
	}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var onDisk Settings
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if onDisk.Mode != ModeWriteOnly {
		t.Errorf("on-disk mode = %q", onDisk.Mode)
	}
}

func TestModeParseFormatRoundTrip(t *testing.T) {
	for _, mode := range []string{ModeReadWrite, ModeReadOnly, ModeWriteOnly} {
		if got := FormatMode(ParseMode(mode)); got != mode {
			t.Errorf("round trip of %q = %q", mode, got)
		}
	}
	if got := ParseMode("nonsense"); got != nfc.ModeReadWrite {
		t.Errorf("ParseMode(nonsense) = %v, want ModeReadWrite", got)
	}
}

func TestInMemoryStoreWhenConfigDirUnset(t *testing.T) {
	store, err := New("")
	if err != nil {
		t.Fatalf("New(\"\"): %v", err)
	}
	if err := store.Save(Settings{Mode: ModeReadOnly}); err != nil {
		t.Fatalf("Save on in-memory store: %v", err)
	}
	if got := store.Get().Mode; got != ModeReadOnly {
		t.Errorf("mode = %q, want %q", got, ModeReadOnly)
	}
	if got := store.Path(); got != "" {
		t.Errorf("Path() = %q, want empty", got)
	}
}

// A save must not move a field the launcher holds, in either direction: not to
// the value that was asked for, which the agent refused, and not to the one in
// force, which would destroy a preference the operator never edited and only
// applies when the agent is launched without the flag.
func TestExplicitKeepsTheStoredValue(t *testing.T) {
	prev := Settings{
		Mode:                ModeReadWrite,
		CardTypes:           []string{"NTAG215"},
		DevicePath:          "ACS ACR1252U 01 00",
		Port:                9480,
		RequirePairedDevice: false,
		ReaderFeedback:      false,
	}

	next := prev
	next.Mode = ModeReadOnly
	next.CardTypes = []string{"DESFire"}
	next.DevicePath = ""
	next.Port = 9470
	next.RequirePairedDevice = true
	next.ReaderFeedback = true

	Explicit{Port: true, RequirePairedDevice: true}.Keep(&next, prev)

	if next.Port != 9480 {
		t.Errorf("port = %d, want the stored 9480", next.Port)
	}
	if next.RequirePairedDevice {
		t.Error("the requirement was written over a stored preference")
	}

	// And nothing else was held back.
	if next.Mode != ModeReadOnly {
		t.Errorf("mode = %q, want %q", next.Mode, ModeReadOnly)
	}
	if !next.ReaderFeedback {
		t.Error("reader feedback was held back without being explicit")
	}
	if len(next.CardTypes) != 1 || next.CardTypes[0] != "DESFire" {
		t.Errorf("card types = %v, want [DESFire]", next.CardTypes)
	}
}

func TestExplicitAny(t *testing.T) {
	if (Explicit{}).Any() {
		t.Error("an empty Explicit reports something set")
	}
	if !(Explicit{ReaderFeedback: true}).Any() {
		t.Error("a set field is not reported")
	}
}
