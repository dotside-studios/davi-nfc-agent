// Package traymenu builds system tray menus declaratively, and delivers their
// clicks as signals rather than as channels the caller has to poll.
//
// It sits on top of a [Driver] — [Fyne] in production, [NewFake] in tests —
// so a menu can be built, clicked and inspected without a display server.
//
// # Why not use the tray library directly
//
// fyne.io/systray hands out one unbuffered ClickedCh per item and *drops* a
// click when nobody happens to be receiving on it. That pushes every caller
// into the same shape: one goroutine holding a select over every item, which
// stops working as soon as the set of items changes at runtime — the usual
// workaround, polling the dynamic items with a default branch after each
// event, silently loses clicks on exactly the items that change most.
//
// This package keeps a receiver on every item for its whole life, so no click
// is dropped, and fans each one out through [signals.Signal]. Handlers are
// registered where the item is declared:
//
//	menu := traymenu.New(nil)
//	menu.Run(func() {
//	    menu.SetIcon(icon)
//
//	    status := menu.Add("Starting...", traymenu.Tooltip("Agent status"), traymenu.Disabled())
//	    menu.AddSeparator()
//
//	    urls := menu.AddSubmenu("Server URLs")
//	    urls.Add("Copy Client URL", traymenu.OnClick(func() { copyClientURL() }))
//
//	    menu.Add("Quit", traymenu.OnClick(menu.Quit))
//
//	    status.SetTitle("Running")
//	}, onExit)
//
// # Concurrency
//
// Click handlers all run on one dispatch goroutine, in the order the clicks
// arrived. That is what makes it safe for a handler to read and write menu
// state without a lock of its own — but it also means a handler that blocks
// holds up every other menu item, so anything that can wait on the OS (a
// password prompt, a network call) belongs in a goroutine of its own.
//
// Menu state (titles, checkmarks, visibility) may be changed from any
// goroutine, whether or not a handler is running.
package traymenu
