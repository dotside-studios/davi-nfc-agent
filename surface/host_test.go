package surface_test

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// badge is the smallest useful plugin: a menu that follows the card on the
// reader, an address of its own, and an entry that copies something.
type badge struct {
	last *traymenu.Item
}

func (b *badge) Describe() surface.Info {
	return surface.Info{ID: "badge", Title: "Badge Reader"}
}

func (b *badge) Attach(host surface.Host) error {
	menu := host.Menu()

	b.last = menu.Add("Last Badge: none", traymenu.Disabled())
	menu.Add("Copy Last Badge", traymenu.OnClick(func() {
		host.Copy("last badge", host.State().Card.UID)
	}))

	host.Endpoints().Set(surface.Endpoint{ID: "badge", Label: "Badge", URL: "http://localhost:8080/"})

	host.Watch(func(state surface.State) {
		if state.Card.Present {
			b.last.SetTitle("Last Badge: " + state.Card.UID)
			return
		}
		b.last.SetTitle("Last Badge: none")
	})
	return nil
}

func TestAPluginFollowsTheAgentWithoutPolling(t *testing.T) {
	menu, fake := newMenu(t)
	host := surface.NewFakeHost(menu.AddSubmenu("Badge Reader"))

	plugin := &badge{}
	if err := plugin.Attach(host); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	if endpoint, ok := host.Endpoints().Get("badge"); !ok || !endpoint.Running() {
		t.Fatal("the plugin's address was not published")
	}

	host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04A224"}})
	if got := plugin.last.Title(); got != "Last Badge: 04A224" {
		t.Fatalf("menu reads %q, want the card that was just published", got)
	}

	// What the entry copies comes from the state, so it cannot drift from what
	// the label above it reads.
	fake.Find("Badge Reader", "Copy Last Badge").Deliver()
	waitFor(t, "the copy to reach the host", func() bool { return len(host.Copied()) == 1 })
	if got := host.Copied()[0]; got.Value != "04A224" {
		t.Fatalf("copied %q, want the card on the reader", got.Value)
	}

	host.Publish(surface.State{Running: true})
	if got := plugin.last.Title(); got != "Last Badge: none" {
		t.Fatalf("menu reads %q with the card gone", got)
	}
}

func TestFakeHostRecordsWhatAPluginAsksFor(t *testing.T) {
	menu, _ := newMenu(t)
	host := surface.NewFakeHost(menu.AddSubmenu("Badge Reader"))

	host.Logf("opening %s", "the gate")
	if err := host.Open("http://localhost:8080/"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if logs := host.Logs(); len(logs) != 1 || logs[0] != "opening the gate" {
		t.Errorf("logs read %v", logs)
	}
	if opened := host.Opened(); len(opened) != 1 || opened[0] != "http://localhost:8080/" {
		t.Errorf("opened %v", opened)
	}
}

func newMenu(t *testing.T) (*traymenu.Menu, *traymenu.Fake) {
	t.Helper()

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	return menu, fake
}

// waitFor polls until cond holds, for the click delivered the way the platform
// delivers one: down the item's channel, on the menu's own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	for i := 0; i < 1000; i++ {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
