package pairing_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/plugins/pairing"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// fakeServer is a pairing server with no listener behind it.
type fakeServer struct {
	pin      string
	started  int
	stopped  int
	startErr error
}

func (f *fakeServer) PIN() string { return f.pin }

func (f *fakeServer) RotatePIN() string {
	f.pin = "222222"
	return f.pin
}

func (f *fakeServer) Start() error {
	f.started++
	return f.startErr
}

func (f *fakeServer) Stop() { f.stopped++ }

func newPairing(t *testing.T, server *fakeServer) *plugin.Harness {
	t.Helper()

	host := plugin.NewHarness(pairing.New(pairing.Config{Server: server, Port: 9472}))
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func TestPairingPublishesItsPageAndPIN(t *testing.T) {
	server := &fakeServer{pin: "111111"}
	host := newPairing(t, server)

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if server.started != 1 {
		t.Fatalf("the pairing server was started %d times", server.started)
	}

	// Both are on this plugin's own menu. Nothing else on the tray knows what a
	// pairing page is, or has to be changed for one to appear.
	if host.Tray.Find("Pair a Phone", "Pairing PIN: 111111") == nil {
		t.Fatalf("the menu does not show the PIN:\n%s", host.Render())
	}

	page := pageRow(t, host)
	if !strings.Contains(page.Title(), "pin=111111") {
		t.Fatalf("the page entry reads %q, without the PIN a phone is asked for", page.Title())
	}
}

// pageRow is the entry showing where the pairing page is.
func pageRow(t *testing.T, host *plugin.Harness) *traymenu.FakeItem {
	t.Helper()

	for _, item := range host.Tray.Find("Pair a Phone").Children() {
		if strings.HasPrefix(item.Title(), "Page: ") {
			return item
		}
	}

	t.Fatalf("no page entry on the menu:\n%s", host.Render())
	return nil
}

func TestRotatingThePINMovesEverythingShowingIt(t *testing.T) {
	server := &fakeServer{pin: "111111"}
	host := newPairing(t, server)

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	label := host.Tray.Find("Pair a Phone", "Pairing PIN: 111111")
	page := pageRow(t, host)

	host.Tray.Find("Pair a Phone", "Regenerate Pairing PIN").Deliver()

	// Waiting on both, in no particular order: everything showing the old PIN
	// has to move, and which entry the plugin redraws first is its business.
	waitFor(t, "the rotated PIN to reach the menu", func() bool {
		return label.Title() == "Pairing PIN: 222222" && strings.Contains(page.Title(), "pin=222222")
	})
}

func TestCopyEntriesHandOutWhatTheMenuShows(t *testing.T) {
	server := &fakeServer{pin: "111111"}
	host := newPairing(t, server)

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	host.Tray.Find("Pair a Phone", "Copy Pairing PIN").Deliver()
	pageRow(t, host).Deliver()
	waitFor(t, "both copies", func() bool { return len(host.Copied()) == 2 })

	// Both entries hand out the same PIN the label is showing, whichever click
	// the tray delivers first.
	var pin, page string
	for _, copied := range host.Copied() {
		if strings.HasPrefix(copied.Value, "http://") {
			page = copied.Value
			continue
		}
		pin = copied.Value
	}
	if pin != "111111" {
		t.Errorf("copied PIN %q", pin)
	}
	if !strings.Contains(page, "pin=111111") {
		t.Errorf("copied URL %q", page)
	}
}

func TestStoppingWithdrawsThePage(t *testing.T) {
	server := &fakeServer{pin: "111111"}
	host := newPairing(t, server)

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := host.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if server.stopped != 1 {
		t.Fatalf("the pairing server was stopped %d times", server.stopped)
	}
	// The menu keeps its place and says so, rather than disappearing.
	if got := pageRow(t, host).Title(); got != "Page: Not running" {
		t.Errorf("a stopped pairing server still offers %q", got)
	}
	if host.Tray.Find("Pair a Phone", "Pairing PIN: 111111") == nil {
		t.Error("the menu went away with the listener")
	}
}

func TestPairingWithoutAServerDoesNotWireUp(t *testing.T) {
	host := plugin.NewHarness(pairing.New(pairing.Config{Port: 9472}))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err == nil {
		t.Fatal("the plugin wired itself up with no pairing server to run")
	}
	if host.Tray.Find("Pair a Phone") != nil {
		t.Error("a menu was left on the tray for a feature that is not there")
	}
}

func TestAPairingServerThatWillNotStart(t *testing.T) {
	server := &fakeServer{pin: "111111", startErr: errors.New("port already bound")}
	host := newPairing(t, server)

	if err := host.Start(); err == nil {
		t.Fatal("Start reported no error though the listener never came up")
	}
	// Nothing is answering, so nothing is offered.
	if got := pageRow(t, host).Title(); got != "Page: Not running" {
		t.Errorf("an address was offered for a listener that never started: %q", got)
	}
}

// waitFor polls until cond holds, for a click delivered the way the platform
// delivers one: down the item's channel, on the menu's own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
