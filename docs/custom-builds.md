# Custom Builds

The agent is a reader, its settings and the stores behind them. Everything that
*serves* something is a plugin: the WebSocket endpoints, the pairing server, the
control center. They are registered by `main.go` and by nothing else, so a build
can leave any of them out — and an application built on this agent plugs in the
same way they do.

## What a build is made of

| | |
|---|---|
| `agent.go` and friends | the reader, the settings, the origin and device stores |
| [`plugins/wsserver`](../plugins/wsserver) | the single listener a device and a web page connect to, and the device and client handlers behind it |
| [`plugins/pairing`](../plugins/pairing) | the phone-pairing server, its PIN, and the tray menu for both |
| `plugin_console.go` | the control center, mounted on the agent's port |
| yours | anything else |

The wiring is in `main.go`:

```go
host := agent.Plugins()

host.Use(wsserver.New(wsserver.Config{Agent: &servingAgent{agent: agent}}))
host.Use(pairing.New(pairing.Config{Server: bootstrapServer, Port: bootstrapPortFlag}))
```

## Leaving something out

Drop a `Use` line and that feature is not in the build. Without `wsserver` the
agent has no WebSocket endpoints: it still opens the reader, holds the settings
and shows a tray, and whatever you registered instead is what serves it. Nothing
outside `main.go` refers to the plugin — `agent.go` asks its plugins what they
can do rather than which one they are:

```go
if serving, ok := plugin.Find[interface{ Port() int }](host); ok { ... }
```

The control center also comes out with a build tag, since it carries an embedded
frontend: `go build -tags nowebui .`. See
[Control Center → Leaving it out](control-center.md#leaving-it-out).

## Writing a plugin

Everything below is `github.com/dotside-studios/davi-nfc-agent/plugin`.

`Describe` is the only method a plugin must have. Every phase is an optional
interface, so implement the ones you have work in:

| Phase | When | Interface |
|---|---|---|
| `Init` | before anything serves: fill a menu, declare an address, find a peer | `Initer` |
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
`fyne.io/systray`. In a build with no tray the menu is discarded and the plugin
runs unchanged.

A plugin that needs more of the agent than `Context` carries — the reader, the
API secret, the origin policy — states what it needs as an interface of its own
and takes it in its config. `wsserver.Agent` is the example, implemented by
`wsserver_host.go` in `package main`, the way the console states `webui.Host`.
Read each value again per call rather than caching it: a listener that comes back
after a rotated secret has to come back with the new one.

## Registering from your own package

Go has no portable dynamic loading, so a plugin is compiled in. Register it from
an init function and no file in the agent changes:

```go
func init() { plugin.Register(&Plugin{gate: OpenGate()}) }
```

`main.go` takes the default registry up at startup, after its own plugins. To
control where yours sits in the start order instead, call `host.Use` directly.

## Serving HTTP on the agent's port

A plugin with a page or an API of its own does not open a listener for it:

```go
func (p *Plugin) Routes() []plugin.Route {
    return []plugin.Route{{
        Pattern: "/turnstile/",
        Handler: p.mux,
        Label:   "Gate",   // and put its address on the menus
    }}
}
```

Whatever is serving the agent's port mounts it, so the page is reachable wherever
the agent already is, under the certificate a device already trusts. The control
center is served this way and has no other mechanism.

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

## Addresses

A `Label` on a route is all a mounted page needs. The address is built by
whatever bound the port — it knows the scheme, the host and the port that was
actually taken, none of which the plugin can be sure of — and withdrawn again
when that listener stops. Leave the label off a route nobody is meant to be
handed: the control center is opened with a token, not a URL, so it has none.

Register an address directly only when there is no route to hang it on — a
listener of your own, or an address that is not a plain path:

```go
ctx.Endpoints().Set(plugin.Endpoint{
    ID:      "turnstile",
    Label:   "Gate",
    URL:     "http://" + hostPort + "/",
    Tooltip: "The gate's own console. Click to copy",
})
```

Either way it appears under **Server URLs** beside the agent's own and is copied
by the same entry. The tray draws whatever is in the register; it is not told
what any of it is.

An empty `URL` means the server behind it is not running: the entry keeps its
place and reads as `Not running` rather than disappearing. Setting the same ID
again replaces it in place, which is how the pairing page survives a PIN rotation
without moving on the menu.

`wsserver` is the one that has to do this by hand, since its addresses are
`ws://` and carry the device mode. It declares both entries in `Init`, before
anything is listening, and fills in the URLs in `Start` from the port it actually
bound. These get pasted into a device, and an address naming an unbound port is
worse than none.

## Following the agent

`ctx.Watch` hands you a snapshot whenever the agent changes. Keep the items you
made and change them; nothing polls, and nothing is redrawn wholesale.

| Field | |
|---|---|
| `Running` | whether the reader is up |
| `Device` | the reader in use, empty when there is none |
| `Card` | the tag on the reader, if any |
| `Port`, `TLS` | what is being served, and whether over TLS |
| `Paired` | how many devices hold a pairing credential |
| `Settings` | mode, card-type filter, pairing requirement, and the rest |
| `Explicit` | the settings the launcher fixed for this run |

`ctx.State()` returns the last snapshot at any time, so a plugin need not keep a
copy. The agent publishes on every lifecycle and settings change, and otherwise
from one watcher of its own that looks for what nothing announces — a card
arriving at the reader, or leaving it. That runs whether or not the build has a
tray.

Check `Explicit` before offering a control for a setting. A field marked there
belongs to the launcher for the whole run, and the agent will refuse a change to
it; show your entry disabled instead.

## Reaching other plugins

By capability, with `plugin.Find`, or by ID with `ctx.Peer("pairing")`. The agent
itself only ever asks by capability: `ServingPort` asks whatever is serving where
it is, the console asks whatever serves clients for its client list, and the
paired-device requirement reaches whatever admits devices.

## Where a menu goes

No platform can insert a menu item in the middle, so anything added after the
tray is built lands under **Quit**. The tray holds a few top-level menus open
where a feature's menu belongs — beside **Paired Devices** and **Allowed
Origins** — and hands them out as plugins take them.

A menu is taken on first use. A plugin that only serves something never asks for
one, and no empty menu is left behind reading as a feature that does nothing.

## Testing

`plugin.Harness` is a real host — the same lifecycle, the same contexts — over a
tray that records a menu instead of drawing it:

```go
h := plugin.NewHarness(&Plugin{gate: fakeGate()})
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

No desktop and no display server.
[`plugin/example_test.go`](../plugin/example_test.go) is a whole turnstile driven
this way; [`plugins/wsserver/wsserver_test.go`](../plugins/wsserver/wsserver_test.go)
starts the real listener and fetches a plugin's page off it.

## Adding hardware

Readers and tag types are a different seam, below the agent rather than beside
it. See [Extending NFC Support](extending-nfc-support.md).
