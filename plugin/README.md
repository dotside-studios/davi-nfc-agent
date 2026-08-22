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
| `State()` / `Watch(fn)` | what the agent is doing, and a signal raised whenever that changes |
| `Peer(id)` / `Host()` | the plugins around it, by name or by capability |
| `Copy`, `Open`, `Logf` | the clipboard, a browser, and the agent's log |

That is the whole of it. The runtime has no notion of an address, an HTTP route
or anything else a particular plugin does: a plugin that serves something draws
its own menu for it, and one that needs something of a peer asks for it by
capability. The agent's own features have no seam a consumer's cannot use.

Nothing here names `fyne.io/systray`. A build with no tray leaves `Config.Menus`
nil and a plugin that fills a menu still runs — its items go to
`traymenu.Discard()` and behave, they are just not on any tray.

## Reaching other plugins

By capability rather than by name:

```go
if serving, ok := plugin.Find[interface{ Port() int }](host); ok { ... }
```

`Find` returns the first registered plugin implementing the interface. It is how
`Agent.ServingPort` asks what is serving it and how the console finds the client
list; nothing in `agent.go` names a server package.

For more than one — `wsserver` gathering the pages its peers want mounted — walk
`host.Plugins()`, which is in registration order, and assert the capability you
want. `peer.Describe().ID` names the one you found.

`Context.Peer(id)` reaches one by ID, for a plugin that extends another it knows.

## Menus and addresses

A plugin draws its own. `wsserver` owns **Server URLs** — the endpoints it
answers on, the pages mounted on it, the secret they ask for — and `pairing`
owns **Pair a Phone**. There is no shared register and nothing else on the tray
knows what a server address is.

The tray holds a few top-level menus open where a feature's menu belongs and
hands them out through `Config.Menus`. A menu is taken on first use, so a plugin
that only serves something never asks for one and leaves nothing empty behind.
In a build with no tray the menu is `traymenu.Discard()`: the items behave, they
are just not on any tray.

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
h.Opened()   // what it asked a browser to show
```

No desktop, no tray, no display server. See `example_test.go` for a whole
turnstile driven this way.
