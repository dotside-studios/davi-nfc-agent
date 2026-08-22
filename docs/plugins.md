# Plugins

A plugin is a feature that puts itself on the agent's tray: its own menu, its
own address, and whatever it needs to keep both in step with the agent. The
pairing server is one. So is a turnstile application built on top of this agent.

Everything here lives in [`surface/`](../surface), which is free of the tray
library beyond the container a plugin fills, and free of `fyne.io/systray`
entirely.

## Why

The tray is one icon, owned by one process. Every menu the agent has grew inside
the tray package as a result: the servers' addresses, the pairing PIN, the
paired devices are drawn by code that knows what each of them is. A feature
added afterwards had two ways in, and neither was good — edit the tray package,
or do without a menu.

Three questions kept coming back:

- The servers are registered somewhere else. How do they show up on the tray?
- Pairing runs beside the agent. How does it add its own entry?
- A consumer's own application wants a menu. Where does it put it?

They have one answer now. A feature registers itself, is handed a host, and puts
itself on the menu through that.

## The shape of a plugin

```go
package turnstile

import (
    "github.com/dotside-studios/davi-nfc-agent/surface"
    "github.com/dotside-studios/davi-nfc-agent/traymenu"
)

type Plugin struct {
    gate   *Gate
    passes *traymenu.Item
}

func (p *Plugin) Describe() surface.Info {
    return surface.Info{
        ID:      "turnstile",
        Title:   "Turnstile",
        Tooltip: "The gate this agent drives",
    }
}

func (p *Plugin) Attach(host surface.Host) error {
    menu := host.Menu()

    p.passes = menu.Add("Passes today: 0", traymenu.Disabled())

    held := menu.AddCheckbox("Hold Gate Open", false)
    held.OnClick(func() { p.gate.Hold(held.Toggle()) })

    menu.AddSeparator()
    menu.Add("Open Gate Console", traymenu.OnClick(func() {
        if err := host.Open("http://localhost:8080/"); err != nil {
            host.Logf("could not open a browser: %v", err)
        }
    }))
    return nil
}

func init() { surface.Register(&Plugin{gate: OpenGate()}) }
```

`Describe` is read once, when the plugin is registered. `Attach` is called once,
as the tray builds its menu, and everything the plugin puts there stays for the
life of the process: it is never asked to draw itself again.

Go has no portable dynamic loading, so a plugin is compiled in — an init
function in a package the consumer's build imports, and nothing in the agent
needs editing. The agent takes up the default registry at startup along with the
features it ships.

## Showing an address

Anything with an address to hand out registers it instead of drawing it:

```go
host.Endpoints().Set(surface.Endpoint{
    ID:      "turnstile",
    Label:   "Gate",
    URL:     "http://localhost:8080/gate",
    Tooltip: "The gate's own console. Click to copy",
})
```

It appears under **Server URLs** beside the agent's own addresses and is copied
by the same entry that copies theirs. The tray draws whatever is in the
register; it is not told what any of it is.

The agent's own servers use exactly this. `Agent.publishEndpoints` registers the
device and client addresses once the listener is up — the port being served, not
the one configured, since these are pasted into a device and an address naming an
unbound port is worse than none — and `withdrawEndpoints` empties their URLs as
the servers go down, which is what makes a stopped server read as `Not running`
rather than handing out an address that refuses the connection.

An address that changes is the same ID published again, so it keeps its place on
the menu. That is how the pairing page survives a PIN rotation:

```go
func (p *pairingPlugin) publish() {
    p.host.Endpoints().Set(surface.Endpoint{
        ID:    "pairing",
        Label: "Pair Phone",
        URL:   p.url(), // http://host:9472/?pin=123456
    })
    p.pin.SetTitle("Pairing PIN: " + p.server.PIN())
}
```

## Following the agent

A plugin never polls, and is never redrawn wholesale. It keeps the items it
created and changes them when the agent moves:

```go
host.Watch(func(state surface.State) {
    if !state.Card.Present {
        return
    }
    p.passes.SetTitle(fmt.Sprintf("Passes today: %d", p.count(state.Card.UID)))
})
```

`State` is a snapshot rather than a set of deltas, so a plugin cannot render a
half-applied combination of settings, and `host.State()` returns the last one at
any time — a plugin that keeps no copy of its own cannot fall behind.

| | |
|---|---|
| `Running` | whether the reader and servers are up |
| `Device` | the reader in use, empty when there is none |
| `Card` | the tag on the reader, if any |
| `Port`, `TLS` | what is being served, and whether over TLS |
| `Paired` | how many devices hold a pairing credential |
| `Settings` | what the agent is set to: mode, card-type filter, and the rest |
| `Explicit` | the settings the launcher fixed for this run |

It is published wherever the tray already redraws itself: the agent starting or
stopping, a card arriving or leaving, a settings change from the tray or the
console, a device pairing, a restart of the listeners.

`Explicit` is worth reading before offering a control. A setting the launcher
fixed belongs to it for the whole run, and a plugin offering to change one
should show its entry disabled rather than accept a change the agent will
refuse.

## Where a plugin's menu goes

No platform can insert a menu item in the middle, so anything added once the
tray is built lands at the end, under **Quit**. The tray holds
`pluginSlotCount` top-level menus open, declared where a feature's menu belongs
— beside **Paired Devices** and **Allowed Origins** — and hands them out as
plugins take them.

A menu is taken on first use, so a plugin that only publishes an address never
asks for one and never leaves an empty menu on the tray reading as a feature
that does nothing.

## What a plugin cannot do

The host is the whole surface. There is no way through it to the tray itself, to
another plugin's menu, or to the agent's settings: a preference belongs to the
agent, and the tray and the console are the two places it is changed. A plugin
that fails to attach is logged and left out, and one that panics in a click
handler is reported and swallowed rather than taking the tray down with it.

## Testing a plugin

`surface.FakeHost` is a host with no agent behind it, over the tray's own fake
driver:

```go
fake := traymenu.NewFake()
menu := traymenu.New(fake)
defer menu.Close()

host := surface.NewFakeHost(menu.AddSubmenu("Turnstile"))
if err := plugin.Attach(host); err != nil {
    t.Fatal(err)
}

host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04A2"}})

fake.Find("Turnstile", "Hold Gate Open").Click()
fake.Render()   // the menu as text
host.Copied()   // what the plugin put on the clipboard
host.Opened()   // what it asked a browser to show
```

No desktop, no tray, no display server. See
[`surface/example_test.go`](../surface/example_test.go) for the whole of a
turnstile plugin driven this way.
