# traymenu

Declarative system tray menus for Go, with clicks delivered as signals.

`traymenu` sits on top of [`fyne.io/systray`](https://github.com/fyne-io/systray)
and is free of anything else in this repository, so it can be lifted out into a
module of its own.

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

## Clicks

The tray library hands out one unbuffered `ClickedCh` per item and drops a click
when nobody is receiving on it:

```go
select {
case item.ClickedCh <- struct{}{}:
default: // nobody home; the click is gone
}
```

A caller therefore needs one goroutine holding a `select` over every item, which
stops working as soon as the set of items changes at runtime: nothing is
receiving on a device or origin row unless some other item was clicked first.

`traymenu` keeps a receiver on every item for its whole life and fans each click
out through a [`signals.Signal`](../signals). Handlers are declared with the item
they belong to:

```go
item.OnClick(func() { ... })                    // the common case
conn := item.Clicked().Connect(func(i *Item) {  // the signal itself
    log.Println("clicked", i.Title())
})
conn.Disconnect()
```

Handlers all run on one dispatch goroutine, in arrival order, so a handler can
read and write menu state without a lock of its own. A handler that blocks holds
up every other item, so anything that can wait on the OS goes in a goroutine:

```go
menu.Add("Trust This Agent in Browsers", traymenu.OnClick(func() {
    go installCA() // prompts for a password
}))
```

Menu state, titles and checkmarks and visibility, may be changed from any
goroutine.

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
reporting what did not fit rather than truncating quietly:

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

`Fake` is a driver that records a menu instead of drawing one:

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
