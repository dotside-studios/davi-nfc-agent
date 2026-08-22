# Custom Builds

The binary this repository ships is one build of the agent, and it is a small
one:

```
cmd/davi-nfc-agent/main.go    flags, and the Use lines that pick the plugins
```

Everything under it is an ordinary package. A build with a different set of
features is a command of your own, in a repository of your own, importing what
it wants:

```go
package main

import (
    "github.com/dotside-studios/davi-nfc-agent/agent"
    "github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
    "github.com/dotside-studios/davi-nfc-agent/tray"

    "example.com/kiosk/turnstile"
)

func main() {
    a := agent.New(manager)
    ui := tray.New(a, "")
    a.SetQuit(ui.Quit)

    a.Plugins().Use(
        wsserver.New(wsserver.Config{Agent: wsserver.ForAgent(a)}),
        turnstile.New(gate),
    )

    ui.Run()
}
```

That build has the reader, the tray, the WebSocket endpoints and a turnstile. It
has no pairing server and no control center, because it did not ask for them.

## What a build is made of

| | |
|---|---|
| [`agent`](../agent) | the reader, the settings, the origin and device stores. Serves nothing |
| [`tray`](../tray) | the agent's user interface, and where plugins put their menus |
| [`plugins/wsserver`](../plugins/wsserver) | the single listener a device and a web page connect to |
| [`plugins/pairing`](../plugins/pairing) | the phone-pairing server, its PIN and its page |
| [`plugins/console`](../plugins/console) | the control center, mounted on the agent's port |
| [`plugin`](../plugin) | the runtime: lifecycle, state, peers |
| yours | anything else |

The agent drives a reader and holds what an operator has decided. Everything
that *serves* something is a plugin, and the only thing that knows which plugins
exist is the command.

## Leaving something out

Drop a `Use` line. Without `wsserver` there are no WebSocket endpoints: the
agent still opens the reader, holds the settings and shows a tray, and whatever
you registered instead is what serves it. Nothing else in the build refers to
the plugin you dropped.

The control center is the exception: it comes out with a build tag, because it
carries an embedded frontend and leaving it unregistered would keep it in the
binary. `go build -tags nowebui ./cmd/davi-nfc-agent`. See
[Control Center → Leaving it out](control-center.md#leaving-it-out).

A build with no desktop registers no tray. Plugins still run, their menus are
discarded, and `Context.Copy` and `Context.Open` say there is nothing to copy to
or open with.

## Writing a plugin

Everything below is `github.com/dotside-studios/davi-nfc-agent/plugin`.

`Describe` is the only method a plugin must have. Every phase is an optional
interface, so implement the ones you have work in:

| Phase | When | Interface |
|---|---|---|
| `Init` | before anything serves: fill a menu, find a peer | `Initer` |
| `Start` | begin serving; called again after a restart | `Starter` |
| `Stop` | stop serving | `Stopper` |
| `Close` | release what outlives serving, on the way out | `Closer` |

```go
type Plugin struct {
    gate   *Gate
    passes *traymenu.Item
}

func (p *Plugin) Describe() plugin.Info {
    return plugin.Info{ID: "turnstile", Title: "Turnstile", Tooltip: "The gate this agent drives"}
}

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

func (p *Plugin) Start(*plugin.Context) error { return p.gate.Open() }

func (p *Plugin) Stop(*plugin.Context) error { return p.gate.Close() }
```

`Init` and `Start` run in registration order, `Stop` and `Close` in reverse of
it. A plugin that fails `Init` is dropped and the rest carry on; one that fails
`Start` stays registered for the next restart. The ordering rules and what each
phase may assume are in [plugin/README.md](../plugin/README.md).

Nothing above names the tray library beyond the menu entries, and nothing names
`fyne.io/systray`.

A plugin that needs more of the agent than `Context` carries — the reader, the
API secret, the origin policy — states what it needs as an interface of its own
and takes it in its config. `wsserver.Agent` is the example, with
`wsserver.ForAgent` adapting the agent this repository ships. Read each value
again per call rather than caching it: a listener that comes back after a
rotated secret has to come back with the new one.

## Registering without editing the command

A plugin registered from an init function lands in the default registry, which
the command takes up at startup after its own:

```go
func init() { plugin.Register(&Plugin{gate: OpenGate()}) }
```

Importing your package for its side effect is then enough. To control where
yours sits in the start order, call `Use` directly instead.

## Serving HTTP on the agent's port

A plugin with a page or an API of its own does not open a listener for it:

```go
func (p *Plugin) Routes() []wsserver.Route {
    return []wsserver.Route{{
        Pattern: "/turnstile/",
        Handler: p.mux,
        Label:   "Gate",   // and list its address on the server's menu
    }}
}
```

Whatever is serving the agent's port mounts it, so the page is reachable wherever
the agent already is, under the certificate a device already trusts. The control
center is served this way and has no other mechanism.

`Route` belongs to the server that mounts it, not to the runtime — the runtime
has no notion of HTTP. `wsserver` collects the declarations by walking its
peers, so a different server would gather the same ones the same way.

- Patterns are `http.ServeMux` ones: a trailing slash takes the subtree.
- `/ws`, `/health` and `/api/v1/health` are refused and logged, naming the plugin
  that asked. An agent whose `/ws` answers something else is broken.
- `/` is claimable. The agent's banner is only there while nothing else wants it.
- Mounts are not wrapped in CORS, unlike the device and client endpoints. A mount
  is a page or an administrative API, not something another origin should be
  fetching and reading.

The pairing server is the counter-example: it runs a listener of its own, over
plain HTTP, because a phone that has not installed the agent's certificate
authority yet is exactly who its page is for.

## Menus and addresses

A plugin draws its own. `wsserver` owns **Server URLs** — the endpoints it
answers on, the pages mounted on it, and the API secret they ask for — and
`pairing` owns **Pair a Phone**. There is no shared register of addresses, and
nothing else on the tray knows what a server address is.

Keep a row and relabel it as the thing behind it comes and goes, rather than an
entry that vanishes:

```go
p.page = ctx.Menu().Add("Page: Not running",
    traymenu.Tooltip("Where the gate is. Click to copy"),
    traymenu.OnClick(func() { ctx.Copy("gate URL", p.url) }),
)
```

If your page is mounted on the agent's listener, a `Label` on the route is
enough: the server lists it, with the address built from the port it bound.

## Following the agent

`ctx.Watch` hands you a snapshot whenever the agent changes. Keep the items you
made and change them; nothing polls, and nothing is redrawn wholesale.

| Field | |
|---|---|
| `Running` | whether the reader is up |
| `Device` | the reader in use, empty when there is none |
| `Card` | the tag on the reader, if any |
| `Paired` | how many devices hold a pairing credential |
| `Settings` | mode, card-type filter, pairing requirement, and the rest |
| `Explicit` | the settings the launcher fixed for this run |

`ctx.State()` returns the last snapshot at any time, so a plugin need not keep a
copy. The agent publishes on every lifecycle and settings change, and otherwise
from one watcher of its own that looks for what nothing announces — a card
arriving at the reader, or leaving it. That runs whether or not the build has a
tray, and the tray reads its own controls from it like everything else.

The state carries what the *agent* knows and nothing about what a plugin is
doing. For that, ask the plugin.

Check `Explicit` before offering a control for a setting. A field marked there
belongs to the launcher for the whole run, and the agent will refuse a change to
it; show your entry disabled instead.

## Reaching other plugins

By capability with `plugin.Find`, by ID with `ctx.Peer("pairing")`, or by walking
`ctx.Host().Plugins()` and asserting the interface you want — which is what the
server does to collect its mounts, and what the console does to find the port it
is served on and the clients connected to it.

The agent itself asks its plugins for nothing. What a plugin needs to know, it
learns from the state; what it needs done, it does. That is the direction the
reach runs in, and it is why a plugin can be replaced or left out.

## Where a menu goes

No platform can insert a menu item in the middle, so anything added once the tray
is built lands under **Quit**. The tray holds `pluginSlotCount` top-level menus
open, right below the status line where **Server URLs** has always been, and
hands them out as plugins take them.

A menu is taken on first use, so a plugin that only serves something never asks
for one and no empty menu is left behind reading as a feature that does nothing.

## Testing

`plugin.Harness` is a real host — the same lifecycle, the same contexts — over a
tray that records a menu instead of drawing one:

```go
h := plugin.NewHarness(&Plugin{gate: fakeGate()})
defer h.Close()

h.Init()
h.Start()
h.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})

h.Tray.Find("Turnstile", "Hold Gate Open").Deliver()
h.Render()   // the menu as text
h.Copied()   // what it put on the clipboard
h.Opened()   // what it asked a browser to show
```

No desktop and no display server.
[`plugin/example_test.go`](../plugin/example_test.go) is a whole turnstile driven
this way; [`plugins/wsserver/wsserver_test.go`](../plugins/wsserver/wsserver_test.go)
starts the real listener and fetches a plugin's page off it.

## Adding hardware

Readers and tag types are a different seam, below the agent rather than beside
it. See [Extending NFC Support](extending-nfc-support.md).
