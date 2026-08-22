# plugin

What the agent is assembled from.

The agent proper is a reader, its settings and the stores behind them. Everything
that *serves* something is a plugin: the WebSocket endpoints a device and a web
page connect to, the pairing server a phone pairs against, the control center,
and whatever a consumer builds on top. A build that wants none of them leaves
them out, and the agent still reads cards.

```go
host := agent.Plugins()

host.Use(
    wsserver.New(wsserver.Config{Agent: servingAgent{agent}}),
    pairing.New(pairing.Config{Server: bootstrap, Port: 9472}),
    &turnstile{gate: gate},
)
```

## Lifecycle

A plugin declares only the phases it has work in. Identity is the only thing
required, so a plugin that just adds a menu implements two methods:

| Phase | When | Interface |
|---|---|---|
| `Init` | once, before anything serves: fill a menu, declare an address, publish routes, find a peer | `Initer` |
| `Start` | begin serving; called again after a restart | `Starter` |
| `Stop` | stop serving, the inverse of `Start` | `Stopper` |
| `Close` | release what outlives serving, on the way out | `Closer` |

`Init` and `Start` run in registration order; `Stop` and `Close` in the reverse
of it, so a plugin registered after the thing it depends on is started after it
and stopped before it. `Host.Start` wires up anything `Init` has not reached, so
a host that is started without being wired up first still runs.

A plugin that fails `Init` is **dropped** — never started, and the rest carry on
without it. One that fails `Start` stays registered, so a later `Restart` can try
it again. `Restart(ids...)` takes only what it is named, or everything when it is
named nothing: that is what a reissued certificate or a rotated secret calls.

Registering a plugin after the host is already running joins it where it is:
wired up and started at once. Registering a second under the same ID replaces
the first, which is stopped and closed on its way out.

## What a plugin gets

A `Context` of its own, and nothing else. There is no way through it to the tray,
to another plugin's menu, or to the agent's settings.

| | |
|---|---|
| `Menu()` | a menu of its own — a [`traymenu.Container`](../traymenu), taken on first use |
| `Endpoints()` | the register of addresses the agent hands out, for one with no route behind it |
| `State()` / `Watch(fn)` | what the agent is doing, and a signal raised whenever that changes |
| `Routes()` | what the plugins want served, for whatever is serving the port |
| `Peer(id)` / `Host()` | the plugins around it |
| `Copy`, `Open`, `Logf` | the clipboard, a browser, and the agent's log |

Nothing here names `fyne.io/systray`. A build with no tray leaves `Config.Menus`
nil and a plugin that fills a menu still runs — its items go to
`traymenu.Discard()` and behave, they are just not on any tray.

## Serving HTTP

A plugin with a page or an API of its own does not open a listener for it. It
implements `RouteProvider`, and whatever is serving the agent's port mounts what
it returns:

```go
func (t *turnstile) Routes() []plugin.Route {
    return []plugin.Route{{Pattern: "/turnstile/", Handler: t.mux, Label: "Gate"}}
}
```

So the gate is reachable at the address a device already trusts, under the
certificate it already has, with no port of its own. The control center is served
exactly this way. A route asking for a path the agent serves itself (`/ws`,
`/health`) is refused and logged; the root is claimable, since the agent's banner
is only there while nothing else wants it.

## Addresses

`Endpoints` is the register of addresses the agent hands out — what the tray
shows under **Server URLs** and copies. Routes and endpoints are not the same
thing: a route is a handler to mount, an endpoint is an address to give someone.
Most mounted pages are both, so a `Label` on a route publishes one, with the URL
built by whatever bound the port rather than by the plugin guessing at the
scheme, the host and the port.

Register one directly when there is no route behind it — a listener of your own,
or an address that is not a plain path:

```go
ctx.Endpoints().Set(plugin.Endpoint{ID: "turnstile", Label: "Gate", URL: gateURL})
```

An empty `URL` means *not running*, which is what a stopped server publishes: the
entry keeps its place and says so, rather than handing out an address that
refuses the connection. Publishing the same ID again — a pairing PIN rotating
into the URL that carries it — keeps that place too.

## Capabilities, not names

The agent asks its plugins what they can do rather than which one they are:

```go
if serving, ok := plugin.Find[interface{ Port() int }](host); ok { ... }
```

That is how `Agent.ServingPort` and the console's client list work, and why the
server can be replaced or left out without the agent knowing.

## Registration from outside

Go has no portable dynamic loading, so a plugin is compiled in. A consumer
registers theirs from an init function in their own package and changes nothing
in the agent; the command line takes the default registry up at startup:

```go
func init() { plugin.Register(&turnstile{gate: OpenGate()}) }
```

## Testing

`Harness` is a real host over a tray that records instead of drawing:

```go
h := plugin.NewHarness(&turnstile{})
defer h.Close()

h.Init()
h.Start()
h.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})

h.Tray.Find("Turnstile", "Hold Gate Open").Deliver()
h.Render()   // the menu as text
h.Copied()   // what the plugin put on the clipboard
h.Endpoints().List()
```

No desktop, no tray, no display server. See `example_test.go` for a whole
turnstile driven this way.
