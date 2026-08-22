# Plugins

The agent is a reader, its settings and the stores behind them. Everything that
*serves* something is a plugin: the WebSocket endpoints, the pairing server, the
control center, and whatever you build on top of them.

That is the whole point of the seam. An application built on this agent — a
turnstile, a kiosk, a badge desk — plugs in the same way the agent's own
features do, and a build that wants none of them leaves them out.

Everything here lives in [`plugin/`](../plugin), with the agent's own plugins
under [`plugins/`](../plugins).

## Why

The tray is one icon, owned by one process, and the servers were built by the
agent itself. Every feature therefore had to be part of the agent: its menu was
drawn by the tray package, its address was computed by the tray package, and its
listener was constructed in `agent.go`. There were two ways to add a feature —
edit those files, or do without.

Three questions kept coming back:

- The servers are registered somewhere else. How do they show up on the tray?
- Pairing runs beside the agent. How does it add its own entry?
- A consumer's own application wants a menu, a page and an address. Where do
  they go?

They have one answer now. Everything that serves registers, is handed a context,
and puts itself where it belongs — including the agent's own servers, which are
a plugin like any other.

## Writing one

```go
package turnstile

import (
    "github.com/dotside-studios/davi-nfc-agent/plugin"
    "github.com/dotside-studios/davi-nfc-agent/traymenu"
)

type Plugin struct {
    gate   *Gate
    passes *traymenu.Item
}

func (p *Plugin) Describe() plugin.Info {
    return plugin.Info{ID: "turnstile", Title: "Turnstile", Tooltip: "The gate this agent drives"}
}

// Init: wire up. A menu of its own, an address, and the agent to follow.
func (p *Plugin) Init(ctx *plugin.Context) error {
    menu := ctx.Menu()

    p.passes = menu.Add("Passes today: 0", traymenu.Disabled())
    held := menu.AddCheckbox("Hold Gate Open", false)
    held.OnClick(func() { p.gate.Hold(held.Toggle()) })

    ctx.Watch(func(state plugin.State) {
        if state.Card.Present {
            p.admit(state.Card.UID)
        }
    })
    return nil
}

// Routes: served on the agent's own listener, so the gate needs no port,
// no certificate and no trust of its own.
func (p *Plugin) Routes() []plugin.Route {
    return []plugin.Route{{Pattern: "/turnstile/", Handler: p.mux}}
}

// Start and Stop: whatever the plugin runs of its own.
func (p *Plugin) Start(ctx *plugin.Context) error {
    if err := p.gate.Open(); err != nil {
        return err
    }
    ctx.Endpoints().Set(plugin.Endpoint{ID: "turnstile", Label: "Gate", URL: p.url()})
    return nil
}

func (p *Plugin) Stop(ctx *plugin.Context) error {
    ctx.Endpoints().SetURL("turnstile", "")
    return p.gate.Close()
}

func init() { plugin.Register(&Plugin{gate: OpenGate()}) }
```

Nothing there names the tray library beyond the entries themselves, and nothing
names `fyne.io/systray`. Go has no portable dynamic loading, so a plugin is
compiled in: an init function in a package your build imports, and no edit to
the agent.

Every phase is optional — `Describe` is the only method a plugin must have. See
[plugin/README.md](../plugin/README.md) for the full lifecycle, the ordering
rules, and what happens when a phase fails.

## The agent's own plugins

| Plugin | What it is |
|---|---|
| [`plugins/wsserver`](../plugins/wsserver) | the single listener a device and a web page connect to, and the device and client handlers behind it |
| [`plugins/pairing`](../plugins/pairing) | the phone-pairing server, its PIN, and the menu an operator works both from |
| `consolePlugin` (in `plugin_console.go`) | the control center, mounted on the agent's port through the route seam |

They are registered by the command line, in `main.go`, and by nothing else:

```go
host := agent.Plugins()
host.Use(wsserver.New(wsserver.Config{Agent: &servingAgent{agent: agent}}))
host.Use(pairing.New(pairing.Config{Server: bootstrapServer, Port: bootstrapPortFlag}))
```

Drop a line and that feature is gone from the build — including the servers. The
agent still opens the reader, still holds the settings, still has a tray; it just
serves nothing. What each plugin needs from the agent is one interface stated in
its own package (`wsserver.Agent`), implemented in `wsserver_host.go` the way the
console's `webui.Host` is implemented in `webui_host.go`.

## Addresses

Anything with an address registers it rather than drawing it:

```go
ctx.Endpoints().Set(plugin.Endpoint{
    ID:      "turnstile",
    Label:   "Gate",
    URL:     "http://localhost:9470/turnstile/",
    Tooltip: "The gate's own console. Click to copy",
})
```

It appears under **Server URLs** beside the agent's own addresses and is copied
by the same entry — the tray draws whatever is in the register and is told what
none of it is.

The servers do exactly this. `wsserver` declares its two entries in `Init`,
before anything is listening, and fills in the URLs in `Start` from the port it
actually bound — these are pasted into a device, and an address naming an unbound
port is worse than none. `Stop` empties them, which is what makes a stopped
server read as `Not running` rather than disappearing. An address that changes is
the same ID published again, which is how the pairing page survives a PIN
rotation without losing its place on the menu.

## Serving HTTP without a listener

A plugin implementing `RouteProvider` is mounted on whatever is serving the
agent's port:

```go
func (p *Plugin) Routes() []plugin.Route {
    return []plugin.Route{{Pattern: "/turnstile/", Handler: p.mux}}
}
```

So a page of yours is reachable wherever the agent already is, under the
certificate a device already trusts, with no port, no TLS and no trust story of
its own. The control center is served this way and has no other mechanism.

The rules the listener applies:

- A route asking for a path the agent serves itself — `/ws`, `/health`,
  `/api/v1/health` — is refused and logged, naming the plugin that asked. An
  agent whose `/ws` answers something else is a broken agent, however it was
  configured.
- The root is claimable. The agent's banner is only there while nothing else
  wants it; the console takes it.
- Mounts are not wrapped in CORS, unlike the device and client endpoints: a mount
  is a page or an administrative API, not something another origin should be
  fetching and reading.

The pairing server is the counter-example — it runs a listener of its own, over
plain HTTP, because a phone that has not installed the agent's certificate
authority yet is exactly who its page is for.

## Following the agent

A plugin never polls, and is never redrawn wholesale. It keeps the items it made
and changes them when the agent moves:

```go
ctx.Watch(func(state plugin.State) { ... })
```

| | |
|---|---|
| `Running` | whether the reader is up |
| `Device` | the reader in use, empty when there is none |
| `Card` | the tag on the reader, if any |
| `Port`, `TLS` | what is being served, and whether over TLS |
| `Paired` | how many devices hold a pairing credential |
| `Settings` | what the agent is set to: mode, card-type filter, and the rest |
| `Explicit` | the settings the launcher fixed for this run |

`State` is a snapshot rather than a set of deltas, so a plugin cannot act on a
half-applied combination, and `ctx.State()` returns the last one at any time.

The agent publishes it: on every lifecycle change, on every settings change, and
otherwise from one watcher of its own (`Agent.WatchState`) that looks for what
nothing announces — a card arriving at the reader, or leaving it. One watcher for
every plugin, rather than one per plugin, and it runs whether or not this build
has a tray.

`Explicit` is worth reading before offering a control. A setting the launcher
fixed belongs to it for the whole run, and a plugin offering to change one should
show its entry disabled rather than accept a change the agent will refuse.

## Reaching other plugins

By capability, not by name:

```go
if serving, ok := plugin.Find[interface{ Port() int }](ctx.Host()); ok { ... }
```

That is how the agent finds the port it is being served on, how the console finds
the client list, and how the paired-device requirement reaches the running device
endpoint. Nothing in `agent.go` names `wsserver`.

`ctx.Peer("pairing")` reaches one by ID, for a plugin that extends another it
knows.

## Where a plugin's menu goes

No platform can insert a menu item in the middle, so anything added once the tray
is built lands at the end, under **Quit**. The tray holds `pluginSlotCount`
top-level menus open, declared where a feature's menu belongs — beside **Paired
Devices** and **Allowed Origins** — and hands them out as plugins take them.

A menu is taken on first use, so a plugin that only serves something never asks
for one and never leaves an empty menu behind reading as a feature that does
nothing. In a build with no tray the menu is discarded, and the plugin neither
knows nor cares.

## Testing

`plugin.Harness` is a real host — the same lifecycle, the same contexts — over a
tray that records a menu instead of drawing one:

```go
h := plugin.NewHarness(&turnstile{})
defer h.Close()

h.Init()
h.Start()
h.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})

h.Tray.Find("Turnstile", "Hold Gate Open").Deliver()
h.Render()             // the menu as text
h.Copied()             // what it put on the clipboard
h.Endpoints().List()   // what it published
h.Routes()             // what it asked to serve
```

No desktop, no tray, no display server. [`plugin/example_test.go`](../plugin/example_test.go)
is a whole turnstile driven this way, and [`plugins/wsserver/wsserver_test.go`](../plugins/wsserver/wsserver_test.go)
starts the real listener and fetches a plugin's page off it.
