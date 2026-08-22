// Package surface is how a feature puts itself on the agent's tray.
//
// The tray is one icon owned by one process, drawn by fyne.io/systray behind
// [traymenu]. A feature that reached for either would have to be built into the
// code that owns the icon, which is why every menu the agent has grew inside
// the tray package itself: the servers' addresses, the pairing PIN and the
// paired devices are all drawn by code that knows what each of them is.
//
// A Plugin turns that around. It says what it is, is handed a [Host], and puts
// its own entries on the menu through that:
//
//	type turnstile struct{ gate *Gate }
//
//	func (t *turnstile) Describe() surface.Info {
//	    return surface.Info{ID: "turnstile", Title: "Turnstile"}
//	}
//
//	func (t *turnstile) Attach(host surface.Host) error {
//	    menu := host.Menu()
//	    held := menu.AddCheckbox("Hold Gate Open", false)
//	    held.OnClick(func() { t.gate.Hold(held.Toggle()) })
//
//	    last := menu.Add("Last Badge: none", traymenu.Disabled())
//	    host.Watch(func(state surface.State) {
//	        if state.Card.Present {
//	            last.SetTitle("Last Badge: " + state.Card.UID)
//	        }
//	    })
//	    return nil
//	}
//
// A plugin never sees the tray library. It gets a [traymenu.Container] to fill,
// a snapshot of what the agent is doing and a signal that raises whenever that
// changes, and the few things a menu entry needs to do outside the menu: the
// clipboard, a browser, and the log.
//
// # Addresses
//
// Anything with an address to hand out registers an [Endpoint] rather than
// drawing one. The device and client servers do it as they start, the pairing
// server does it for its own page, and a plugin serving something of its own
// does it the same way, so all of them appear together on the menu and are
// copied by the same entry.
//
// # Reactivity
//
// A plugin does not poll and is not redrawn for it. It keeps the items it
// created and changes them when [Host.Watch] says the agent has moved: menu
// state may be set from any goroutine, and a handler runs on the tray's own
// dispatch goroutine in the order clicks arrived.
//
// # Registration
//
// Go has no portable dynamic loading, so a plugin is compiled in. A consumer
// adds theirs to the default registry from an init function and needs no other
// change to the agent:
//
//	func init() { surface.Register(&turnstile{gate: OpenGate()}) }
//
// The agent builds its own registry from the default one plus the features it
// ships, and hands each plugin to the tray as it is attached.
package surface
