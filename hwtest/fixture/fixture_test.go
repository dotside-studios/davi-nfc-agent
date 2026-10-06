package fixture_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
)

const testdata = "testdata"

func lrpFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := fixture.Find(testdata, fixture.KindLRP)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no LRP fixtures under testdata")
	}
	return paths
}

// Every committed LRP fixture, whether generated from the emulator or captured
// from a card by the hardware tests, must replay through ev2.LRPSession.
func TestReplayCommittedLRPFixtures(t *testing.T) {
	for _, path := range lrpFixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := fixture.LoadLRP(path)
			if err != nil {
				t.Fatal(err)
			}
			stats, err := fixture.ReplayLRP(f)
			if errors.Is(err, fixture.ErrNoKeys) {
				t.Skip("recorded without keys")
			}
			if err != nil {
				t.Fatal(err)
			}
			if stats.Authentications == 0 {
				t.Errorf("no authentication was reproduced: %+v", stats)
			}
			t.Logf("%+v", stats)
		})
	}
}

// A replay that cannot fail proves nothing, so each kind of damage to a good
// fixture must be caught.
func TestReplayLRPCatchesDamage(t *testing.T) {
	path := lrpFixtures(t)[0]
	damage := map[string]func(f *fixture.LRP){
		"a wrong key": func(f *fixture.LRP) { f.Keys["0"] = strings.Repeat("00", 16) },
		"a changed final answer of the card": func(f *fixture.LRP) {
			w := lrpWire(f, func(w fixture.Wire) bool { return strings.HasPrefix(w.Command, "90AF") })
			w.Response = flipNibble(w.Response, 4)
		},
		"a changed reader MAC": func(f *fixture.LRP) {
			w := lrpWire(f, func(w fixture.Wire) bool { return strings.HasPrefix(w.Command, "90AF") })
			w.Command = flipNibble(w.Command, 10+40)
		},
		"a changed secure command": func(f *fixture.LRP) {
			w := lrpWire(f, secureData)
			w.Command = flipNibble(w.Command, 12)
		},
		"a changed secure answer": func(f *fixture.LRP) {
			w := lrpWire(f, secureData)
			w.Response = flipNibble(w.Response, 2)
		},
		"a changed URL": func(f *fixture.LRP) { f.URLs[0].URL = strings.Replace(f.URLs[0].URL, "mac=", "mac=00", 1) },
	}
	for name, fn := range damage {
		t.Run(name, func(t *testing.T) {
			f, err := fixture.LoadLRP(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.ReplayLRP(f); err != nil {
				t.Fatalf("the undamaged fixture does not replay: %v", err)
			}
			fn(f)
			if _, err := fixture.ReplayLRP(f); err == nil {
				t.Error("the replay accepted a damaged fixture")
			}
		})
	}
}

func TestReplayLRPNeedsKeys(t *testing.T) {
	f, err := fixture.LoadLRP(lrpFixtures(t)[0])
	if err != nil {
		t.Fatal(err)
	}
	f.Keys = nil
	if _, err := fixture.ReplayLRP(f); !errors.Is(err, fixture.ErrNoKeys) {
		t.Errorf("err = %v, want ErrNoKeys", err)
	}
}

// lrpWire returns the first exchange of an LRP step that match accepts.
func lrpWire(f *fixture.LRP, match func(fixture.Wire) bool) *fixture.Wire {
	for si := range f.Steps {
		if f.Steps[si].Suite != fixture.SuiteLRP {
			continue
		}
		for i := range f.Steps[si].Wire {
			if match(f.Steps[si].Wire[i]) {
				return &f.Steps[si].Wire[i]
			}
		}
	}
	panic("no matching exchange in the fixture")
}

// secureData matches a native command that is not part of an authentication and
// that the card answered with data and a MAC.
func secureData(w fixture.Wire) bool {
	if !strings.HasPrefix(w.Command, "90") || len(w.Command) < 12 || !strings.HasSuffix(w.Response, "9100") || len(w.Response) < 4+16 {
		return false
	}
	switch w.Command[2:4] {
	case "71", "77", "AF":
		return false
	}
	return true
}

// flipNibble changes the hex digit at index i.
func flipNibble(hex string, i int) string {
	c := hex[i]
	if c == '0' {
		c = '1'
	} else {
		c = '0'
	}
	return hex[:i] + string(c) + hex[i+1:]
}

func TestWriteAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &fixture.Raw{
		Version: fixture.Version, Kind: fixture.KindRawFraming,
		Reader: "Some Reader", ATR: "3B8F", Method: "acr122", CanTransceiveRaw: true,
		Tag: fixture.RawTag{UID: "04AABBCCDDEEFF", Type: "NTAG215"},
		Exchanges: []fixture.RawExchange{{
			Name: "READ_SIG", Frame: "3C00", Reply: "00",
			Wire: []fixture.Wire{{Command: "FF000000053C00", Response: "D5430090 00"}},
		}},
	}
	path, err := fixture.Write(dir, "raw-test", want)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("fixture mode = %v, %v, want 0600", info, err)
	}

	got, err := fixture.LoadRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reader != want.Reader || len(got.Exchanges) != 1 || got.Exchanges[0].Wire[0] != want.Exchanges[0].Wire[0] {
		t.Errorf("round trip = %+v", got)
	}

	if _, err := fixture.LoadLRP(path); err == nil {
		t.Error("a raw fixture loaded as an LRP one")
	}
	found, err := fixture.Find(dir, fixture.KindRawFraming)
	if err != nil || len(found) != 1 {
		t.Errorf("Find = %v, %v", found, err)
	}
	if found, err := fixture.Find(filepath.Join(dir, "missing"), fixture.KindLRP); err != nil || len(found) != 0 {
		t.Errorf("Find in a missing directory = %v, %v, want none", found, err)
	}
}

func TestLoadRejectsUnknownFieldsAndVersions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := fixture.LoadRaw(write("a.json", `{"version":1,"kind":"raw-framing","surprise":true}`)); err == nil {
		t.Error("an unknown field was accepted")
	}
	if _, err := fixture.LoadRaw(write("b.json", `{"version":2,"kind":"raw-framing","exchanges":[]}`)); err == nil {
		t.Error("an unknown version was accepted")
	}
}

func TestRecorder(t *testing.T) {
	var r fixture.Recorder
	r.Only("acr")
	r.Connected("Other Reader", []byte{1}, "none", false)
	if _, ok := r.Info(); ok {
		t.Fatal("a reader that does not match was recorded")
	}
	r.Connected("ACS ACR122U", []byte{0x3B, 0x8F}, "acr122", true)
	r.Transmit("Other Reader", []byte{1}, []byte{2}, nil)
	r.Transmit("ACS ACR122U", []byte{0xFF, 0xCA}, []byte{0x90, 0x00}, nil)
	mark := r.Mark()
	r.Transmit("ACS ACR122U", []byte{0x01}, nil, errors.New("gone"))

	info, ok := r.Info()
	if !ok || info.Method != "acr122" || !info.CanTransceiveRaw || fixture.Hex(info.ATR) != "3B8F" {
		t.Errorf("info = %+v, %v", info, ok)
	}
	all := r.Since(0)
	if len(all) != 2 || all[0].Command != "FFCA" || all[0].Response != "9000" {
		t.Errorf("recorded = %+v", all)
	}
	if got := r.Since(mark); len(got) != 1 || got[0].Error != "gone" || got[0].Response != "" {
		t.Errorf("since mark = %+v", got)
	}
}
