package scenario_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/hwtest/scenario"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/nfc/virtualnfc"
)

// update rewrites the committed emulator fixtures. They are generated from
// nfctest's NTAG 424 emulator, never captured from a card, and the emulator and
// the driver share one reading of the datasheet: they prove the harness and the
// replay work, not that the card behaves this way.
var update = flag.Bool("update", false, "rewrite hwtest/fixture/testdata from the emulators")

const (
	emuUID  = "04A1B2C3D4E5F6"
	emuName = "nfctest emulator"
)

func emuKeys() nfc.NTAG424Keys {
	slots := map[byte][]byte{}
	for n := byte(0); n <= 4; n++ {
		slots[n] = bytes.Repeat([]byte{0x10 + n}, 16)
	}
	return nfc.NTAG424Keys{Slots: slots}
}

func emuCard(opts ...nfctest.NTAG424Option) *nfctest.EmulatedCard {
	keys := map[byte][]byte{}
	for n, k := range emuKeys().Slots {
		keys[n] = k
	}
	return nfctest.NTAG424(emuUID, append([]nfctest.NTAG424Option{nfctest.NTAG424WithKeys(keys)}, opts...)...)
}

// lane is a supervisor over one emulated card whose every APDU is recorded.
type lane struct {
	sup *nfc.Supervisor
	rec *fixture.Recorder
	tag scenario.Tag
}

func newLane(t *testing.T, card *nfctest.EmulatedCard) *lane {
	t.Helper()
	rec := &fixture.Recorder{}
	rec.Connected(emuName, nil, "none", false)

	mgr := virtualnfc.NewManager()
	const path = "mock:usb:001"
	dev := virtualnfc.NewDevice(path, virtualnfc.PollMode, "mock")
	mgr.Plug(path, dev)
	dev.Present(virtualnfc.NewDriverCard(rec.Tap(card.Transport(), emuName), card.UID(), nfc.DetectedNTAG424))

	sup, err := nfc.NewSupervisor(mgr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	watch := scenario.Watch(sup)
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sup.Stop(); watch.Close() })

	tag, err := watch.WaitTimeout("", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &lane{sup: sup, rec: rec, tag: tag}
}

func (l *lane) config(keys nfc.NTAG424Keys) scenario.LRPConfig {
	return scenario.LRPConfig{Sup: l.sup, Rec: l.rec, Tag: l.tag, Keys: keys, IncludeKeys: true}
}

// softTB records skips instead of ending the test, so a procedure that skips
// can be examined.
type softTB struct {
	*testing.T
	mu    sync.Mutex
	skips []string
}

func (s *softTB) Skipf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skips = append(s.skips, fmt.Sprintf(format, args...))
}

func TestRunLRPOnACardAlreadyInLRPMode(t *testing.T) {
	l := newLane(t, emuCard(nfctest.NTAG424WithLRP()))
	var out fixture.LRP
	scenario.RunLRP(context.Background(), t, l.config(emuKeys()), &out)

	checkLRPFixture(t, &out)
	if *update {
		writeFixture(t, "lrp-emulator-already-lrp", &out)
	}
}

func TestRunLRPSwitchesOnlyTheNamedTag(t *testing.T) {
	t.Run("named tag is switched", func(t *testing.T) {
		card := emuCard()
		l := newLane(t, card)
		cfg := l.config(emuKeys())
		cfg.EnableUID = strings.ToLower(emuUID)

		var out fixture.LRP
		scenario.RunLRP(context.Background(), t, cfg, &out)

		checkLRPFixture(t, &out)
		if names := stepNames(&out); names[0] != "baseline" || !contains(names, "switch-to-lrp") {
			t.Errorf("steps = %v, want a baseline then the switch", names)
		}
		if *update {
			writeFixture(t, "lrp-emulator-switched", &out)
		}
	})

	for name, uid := range map[string]string{"no uid named": "", "another uid named": "04FFFFFFFFFFFF"} {
		t.Run(name, func(t *testing.T) {
			l := newLane(t, emuCard())
			cfg := l.config(emuKeys())
			cfg.EnableUID = uid

			tb := &softTB{T: t}
			var out fixture.LRP
			scenario.RunLRP(context.Background(), tb, cfg, &out)

			if len(tb.skips) != 1 {
				t.Fatalf("skips = %v, want exactly one", tb.skips)
			}
			if contains(stepNames(&out), "switch-to-lrp") {
				t.Fatal("a tag that was not named was switched")
			}
			caps, err := l.sup.Capabilities(context.Background(), l.tag.Device, l.tag.UID)
			if err != nil || caps.LRP {
				t.Errorf("capabilities = %+v, %v: the tag must still be on AES", caps, err)
			}
		})
	}
}

func checkLRPFixture(t *testing.T, out *fixture.LRP) {
	t.Helper()
	stats, err := fixture.ReplayLRP(out)
	if err != nil {
		t.Fatalf("the fixture the procedure wrote does not replay: %v", err)
	}
	t.Logf("replayed: %+v", stats)
	if stats.Authentications == 0 || stats.Commands == 0 || stats.Responses == 0 {
		t.Errorf("replay checked too little: %+v", stats)
	}
	if len(out.URLs) < 6 || stats.URLs != len(out.URLs) {
		t.Errorf("URLs recorded %d, verified %d, want 6 recorded and all verified", len(out.URLs), stats.URLs)
	}
}

func stepNames(f *fixture.LRP) []string {
	var names []string
	for _, s := range f.Steps {
		names = append(names, s.Name)
	}
	return names
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func writeFixture(t *testing.T, name string, v any) {
	t.Helper()
	path, err := fixture.Write("../fixture/testdata", name, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
