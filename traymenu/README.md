# traymenu

Declarative system tray menus for Go, with clicks delivered as signals.

`traymenu` sits on top of [`fyne.io/systray`](https://github.com/fyne-io/systray)
and is deliberately free of anything else in this repository, so it can be
lifted out into a module of its own.

```go
menu := traymenu.New(nil) // nil driver: the real tray

menu.Run(func() {
    menu.SetIcon(icon)
    menu.SetTooltip("NFC Agent")

    status := menu.Add("Starting...", traymenu.Tooltip("Agent status"), traymenu.Disabled())

    urls := menu.AddSubmenu("Server URLs")
    urls.Add("Copy Client URL", traymenu.OnClick(func() { copyClientURL() }))

    menu.AddSeparator()
    menu.Add("Quit", traymenu.OnClick(menu.Quit))

    status.SetTitle("Running")
}, onExit)
```

## Why

The tray library hands out one unbuffered `ClickedCh` per item, and **drops a
click when nobody happens to be receiving on it**:

```go
select {
case item.ClickedCh <- struct{}{}:
default: // nobody home; the click is gone
}
```

Every caller therefore ends up writing the same loop — one goroutine holding a
`select` over every item — and that loop stops working the moment the set of
items changes at runtime. The usual workaround is to poll the dynamic items
with a `default` branch after each event:

```go
for {
    select {
    case <-mStart.ClickedCh:
        start()
    case <-mQuit.ClickedCh:
        return
    }

    // ...and now poll everything the select could not name.
    for _, filter := range cardTypeFilters {
        select {
        case <-filter.item.ClickedCh:
            toggle(filter)
        default:
        }
    }
}
```

Which loses clicks on exactly the items that change most: nothing is receiving
on a device or origin row unless some *other* item was clicked first.

`traymenu` keeps a receiver on every item for its whole life, so no click is
dropped, and fans each one out through a signal. Handlers are declared with the
item they belong to, so an entry can be read — and moved — without
cross-referencing an event loop somewhere else.

## The signals pattern

A [`signals.Signal[T]`](../signals) is a fan-out point: any number of handlers
connect, and every `Emit` calls them all. It is the inverse of a channel — a
channel has one value and races its receivers for it; a signal has many
receivers and gives each of them the value.

```go
item.OnClick(func() { ... })                    // the common case
conn := item.Clicked().Connect(func(i *Item) {  // the signal itself
    log.Println("clicked", i.Title())
})
conn.Disconnect()
```

Click handlers all run on **one dispatch goroutine**, in arrival order. That is
what makes it safe for a handler to read and write menu state without a lock of
its own — and it means a handler that blocks holds up every other item, so
anything that can wait on the OS belongs in a goroutine:

```go
menu.Add("Trust This Agent in Browsers", traymenu.OnClick(func() {
    go installCA() // prompts for a password
}))
```

Menu state — titles, checkmarks, visibility — may be changed from any goroutine.

## Radio groups

Three checkboxes that are really one choice are a `Radio`, keyed by a value of
your own type. `Set` moves the tick silently, for a change made elsewhere;
clicking raises `Selected`.

```go
modes := traymenu.NewRadio[nfc.ReaderMode](menu.AddSubmenu("Mode"))
modes.Add(nfc.ModeReadWrite, "Read/Write Mode")
modes.Add(nfc.ModeReadOnly, "Read Only Mode")
modes.Set(nfc.ModeReadWrite)

modes.OnSelect(func(mode nfc.ReaderMode) { reader.SetMode(mode) })
```

## Lists

No supported platform can remove a menu item once it is added. A `List` takes a
fixed pool of items up front and relabels and hides them as the contents change,
which is the trick every dynamic tray menu ends up reinventing — written once,
with the cap made explicit rather than silently truncating:

```go
origins := traymenu.NewList[string](menu.AddSubmenu("Allowed Origins"), 8, traymenu.Checkbox(false))
origins.OnActivate(func(row traymenu.Row[string]) { revoke(row.Value) })

if dropped := origins.Set(rows); dropped > 0 {
    log.Printf("%d origins do not fit on the menu", dropped)
}
```

`Row[T].Value` carries whatever the handler needs back, so it does not have to
look the row up again by its label.

## Testing

`Fake` is a driver that records a menu instead of drawing one, which is what
makes a tray menu testable at all:

```go
fake := traymenu.NewFake()
menu := traymenu.New(fake)
defer menu.Close()

buildMenu(menu)

fake.Find("Mode: Read/Write", "Read Only Mode") // by title, down the tree
fake.Render()                                   // the whole tree as text

item.Click()      // runs the handlers and waits for them
native.Deliver()  // or drive the platform path, click channel and all
```

`Item.Click` refuses a disabled or hidden item, as a real tray would.
