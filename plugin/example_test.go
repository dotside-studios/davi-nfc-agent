package plugin_test

import (
	"fmt"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// turnstile is a consumer's own feature: it opens a gate when a badge is read,
// serves a page of its own, and wants both on the agent it is built on.
type turnstile struct {
	gate   gate
	held   *traymenu.Item
	passes *traymenu.Item
	count  int
	open   bool
}

// gate stands in for the hardware.
type gate struct{}

func (gate) Open() error  { return nil }
func (gate) Close() error { return nil }

func (t *turnstile) Describe() plugin.Info {
	return plugin.Info{ID: "turnstile", Title: "Turnstile", Tooltip: "The gate this agent drives"}
}

// Init fills the menu and follows the reader. Nothing here names the tray
// library beyond the entries themselves, and nothing names what draws them.
func (t *turnstile) Init(ctx *plugin.Context) error {
	menu := ctx.Menu()

	t.passes = menu.Add("Passes today: 0", traymenu.Disabled())
	t.held = menu.AddCheckbox("Hold Gate Open", false)
	t.held.OnClick(func() { t.open = t.held.Toggle() })

	menu.AddSeparator()
	menu.Add("Copy Gate URL", traymenu.OnClick(func() {
		ctx.Copy("gate URL", "http://localhost:9470/turnstile/")
	}))

	// A badge on the reader is a pass through the gate.
	ctx.Watch(func(state plugin.State) {
		if !state.Card.Present {
			return
		}
		t.count++
		t.passes.SetTitle(fmt.Sprintf("Passes today: %d", t.count))
	})
	return nil
}

// Routes are served on the agent's own listener, so the gate needs no port, no
// certificate and no trust of its own. Labelling one puts its address on the
// agent's menus, built by whatever bound the port.
func (t *turnstile) Routes() []plugin.Route {
	return []plugin.Route{{
		Pattern: "/turnstile/",
		Handler: http.NotFoundHandler(),
		Label:   "Gate",
	}}
}

// Start is where a plugin with something of its own to run would run it.
func (t *turnstile) Start(*plugin.Context) error { return t.gate.Open() }

func (t *turnstile) Stop(*plugin.Context) error { return t.gate.Close() }

// A plugin is registered, wired up, started and stopped by the agent, and never
// asked to draw itself again: it keeps the items it made and changes them as
// the agent moves. This is the whole of what the agent does with one, with a
// harness standing in for the tray.
func Example() {
	host := plugin.NewHarness(&turnstile{})
	defer host.Close()

	_ = host.Init()
	_ = host.Start()

	// Two badges pass the reader.
	host.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})
	host.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04B7"}})

	for _, route := range host.Routes() {
		fmt.Printf("%s serves %s as %q\n", route.Owner, route.Pattern, route.Label)
	}
	fmt.Print(host.Render())

	// Output:
	// turnstile serves /turnstile/ as "Gate"
	// Turnstile
	//   Passes today: 2 (disabled)
	//   [ ] Hold Gate Open
	//   ----
	//   Copy Gate URL
}
