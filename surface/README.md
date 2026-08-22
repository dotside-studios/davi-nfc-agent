# surface

The agent's plugin surface: how a feature puts itself on the tray without
knowing what draws it.

There is one tray icon per process, owned by the code that built it, so every
menu the agent had grew inside the tray package: the addresses, the pairing PIN,
the paired devices are drawn by code that knows what each of them is. A feature
added later had nowhere to go.

A plugin says what it is, is handed a `Host`, and puts its own entries on the
menu through that.

```go
type turnstile struct{ gate *Gate }

func (t *turnstile) Describe() surface.Info {
    return surface.Info{ID: "turnstile", Title: "Turnstile"}
}

func (t *turnstile) Attach(host surface.Host) error {
    menu := host.Menu()

    held := menu.AddCheckbox("Hold Gate Open", false)
    held.OnClick(func() { t.gate.Hold(held.Toggle()) })

    last := menu.Add("Last Badge: none", traymenu.Disabled())
    host.Watch(func(state surface.State) {
        if state.Card.Present {
            last.SetTitle("Last Badge: " + state.Card.UID)
        }
    })
    return nil
}

func init() { surface.Register(&turnstile{gate: OpenGate()}) }
```

Nothing there names the tray library beyond the items themselves, and nothing
names `fyne.io/systray` at all.

## What a plugin gets

`Host` is the whole of it:

| | |
|---|---|
| `Menu()` | the plugin's own top-level menu, a [`traymenu.Container`](../traymenu) to fill |
| `Endpoints()` | the register of addresses the agent hands out |
| `State()` / `Watch(fn)` | what the agent is doing, and a signal raised whenever that changes |
| `Copy`, `Open`, `Logf` | the clipboard, a browser, and the agent's log |

## Addresses

A feature with an address to hand out registers it rather than drawing it:

```go
host.Endpoints().Set(surface.Endpoint{
    ID:    "turnstile",
    Label: "Gate",
    URL:   "http://localhost:8080/gate",
})
```

It appears under **Server URLs** beside the device and client addresses, and is
copied by the same entry that copies theirs. The device and client servers
publish theirs as they start and withdraw them as they stop, which is what makes
a stopped server read as `Not running` rather than handing out an address that
refuses the connection. An address that changes — a pairing PIN rotating into
the URL that carries it — is the same ID published again, so it keeps its place
on the menu.

## Reactivity

A plugin is never asked to redraw itself. It keeps the items it created and
changes them when `Watch` says the agent has moved: the agent starting or
stopping, a card arriving or leaving, a settings change from either surface, a
device pairing, a restart of the listeners.

`State` is a snapshot rather than a set of deltas, so a plugin cannot render a
half-applied combination, and `State()` returns the last one at any time. Menu
state may be set from any goroutine; a handler that can wait on the OS belongs
in a goroutine of its own, since watchers run one at a time.

## Registration

Go has no portable dynamic loading, so a plugin is compiled in. A consumer
registers theirs from an init function in their own package and changes nothing
in the agent; the agent takes up the default registry at startup alongside the
features it ships. Registering twice under one ID replaces rather than
duplicates, which makes registration safe to repeat.

## Testing

`FakeHost` is a host with no agent behind it. It records what the plugin asked
for and lets a test move the agent under it:

```go
fake := traymenu.NewFake()
menu := traymenu.New(fake)
defer menu.Close()

host := surface.NewFakeHost(menu.AddSubmenu("Turnstile"))
plugin.Attach(host)

host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04A2"}})

fake.Render()   // what the plugin drew
host.Copied()   // what it put on the clipboard
```
