package surface_test

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// turnstile is a consumer's own feature: it opens a gate when a badge is read,
// serves a page of its own, and wants both on the agent's tray.
type turnstile struct {
	held   *traymenu.Item
	opened *traymenu.Item
	passes int
}

func (t *turnstile) Describe() surface.Info {
	return surface.Info{
		ID:      "turnstile",
		Title:   "Turnstile",
		Tooltip: "The gate this agent drives",
	}
}

func (t *turnstile) Attach(host surface.Host) error {
	menu := host.Menu()

	t.opened = menu.Add("Passes today: 0", traymenu.Disabled())
	t.held = menu.AddCheckbox("Hold Gate Open", false)
	t.held.OnClick(func() { host.Logf("gate held open: %v", t.held.Toggle()) })

	menu.AddSeparator()
	menu.Add("Copy Gate URL", traymenu.OnClick(func() {
		host.Copy("gate URL", "http://localhost:8080/gate")
	}))

	// Its own address, shown and copied beside the agent's own.
	host.Endpoints().Set(surface.Endpoint{
		ID:    "turnstile",
		Label: "Gate",
		URL:   "http://localhost:8080/gate",
	})

	// And its reactivity: a badge on the reader is a pass through the gate.
	host.Watch(func(state surface.State) {
		if !state.Card.Present {
			return
		}
		t.passes++
		t.opened.SetTitle(fmt.Sprintf("Passes today: %d", t.passes))
	})
	return nil
}

// A plugin puts itself on the tray through the Host it is handed, and never
// touches the tray library's own types. This is the whole of what the agent
// does with it, with a fake host standing in for the tray.
func Example() {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	defer menu.Close()

	// The agent's side: a plugin is registered, then handed its menu and the
	// state to follow.
	registry := surface.NewRegistry()
	if err := registry.Add(&turnstile{}); err != nil {
		fmt.Println(err)
		return
	}

	for _, plugin := range registry.Plugins() {
		info := plugin.Describe()
		host := surface.NewFakeHost(menu.AddSubmenu(info.Name(), traymenu.Tooltip(info.Tooltip)))
		if err := plugin.Attach(host); err != nil {
			fmt.Println(err)
			return
		}

		// Two badges pass the reader.
		host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04A2"}})
		host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04B7"}})

		for _, endpoint := range host.Endpoints().List() {
			fmt.Printf("%s: %s\n", endpoint.Label, endpoint.URL)
		}
	}

	fmt.Print(fake.Render())
	// Output:
	// Gate: http://localhost:8080/gate
	// Turnstile
	//   Passes today: 2 (disabled)
	//   [ ] Hold Gate Open
	//   ----
	//   Copy Gate URL
}
