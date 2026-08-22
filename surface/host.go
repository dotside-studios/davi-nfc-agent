package surface

import (
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Host is everything a plugin gets from the agent, stated as one interface so
// that what a plugin can do is readable in one place.
//
// The tray implements it. A test uses [FakeHost], which records the same calls
// against a menu with no desktop behind it.
type Host interface {
	// Menu is the plugin's own place on the tray: a submenu of its own,
	// titled from its [Info] and shown once the plugin has put something in
	// it. Items may be added at any time, not only during Attach.
	Menu() traymenu.Container

	// Endpoints is the register of addresses the agent hands out. A plugin
	// serving something of its own puts it here rather than drawing it, and
	// the tray shows and copies it beside the agent's own.
	Endpoints() *Endpoints

	// State is what the agent is doing now. A plugin reads it when it needs a
	// value, rather than keeping a copy that can go stale.
	State() State

	// Watch runs fn whenever the state changes, with the new state. It is not
	// called with the current one first: read State for that.
	//
	// Handlers run one at a time, on whichever goroutine reported the change,
	// so a slow one holds up the rest. Menu state may be set from any
	// goroutine, so a handler normally does its work inline; anything that can
	// wait on the OS belongs in a goroutine of its own.
	Watch(fn func(State)) *traymenu.Connection

	// Copy puts a value on the clipboard. what names it for the log, which is
	// the only feedback a tray menu has for a copy.
	Copy(what, value string)

	// Open shows a URL in the operator's browser.
	Open(target string) error

	// Logf writes to the agent's log, tagged with the plugin's ID so a line
	// says which feature wrote it.
	Logf(format string, args ...any)
}

// State is what the agent looks like at one moment, as much of it as a plugin
// has any business seeing. It is a snapshot rather than a set of deltas, so a
// plugin can never render a half-applied combination.
type State struct {
	// Running reports whether the servers and the reader are up.
	Running bool

	// Device is the reader in use, empty when there is none.
	Device string

	// Card is the tag on the reader, if any.
	Card Card

	// Port is the port the agent is serving on, 0 when it is not serving.
	Port int

	// TLS reports whether that port is served over TLS, which decides whether
	// the addresses are ws:// or wss://.
	TLS bool

	// Paired counts the devices holding a pairing credential.
	Paired int

	// Settings is what the agent is set to: reader mode, card-type filter,
	// pairing requirement and the rest. Every preference lives here and
	// nowhere else, so a plugin cannot read one value while the agent acts on
	// another.
	Settings settings.Settings

	// Explicit marks the settings the launcher fixed for this run. A plugin
	// offering a control for one of them should show it disabled rather than
	// accept a change the agent will refuse.
	Explicit settings.Explicit
}

// Card is the tag on the reader.
type Card struct {
	Present bool
	UID     string
	Type    string
}
