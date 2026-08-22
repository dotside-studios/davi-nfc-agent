// Package traymenu builds system tray menus declaratively and delivers their
// clicks as signals.
//
// It runs on a Driver: Fyne in production, NewFake in tests, so a menu can be
// built, clicked and inspected without a display server.
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
// fyne.io/systray drops a click when nobody is receiving on the item's channel,
// which costs clicks on any menu whose items change at runtime. This package
// keeps a receiver on every item for its whole life.
//
// # Concurrency
//
// Click handlers all run on one dispatch goroutine, in arrival order, so a
// handler can read and write menu state without a lock of its own. A handler
// that blocks holds up every other menu item, so anything that can wait on the
// OS belongs in a goroutine.
//
// Menu state may be changed from any goroutine, whether or not a handler is
// running.
package traymenu
