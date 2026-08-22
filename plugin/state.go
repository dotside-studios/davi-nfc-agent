package plugin

import "github.com/dotside-studios/davi-nfc-agent/settings"

// State is what the agent looks like at one moment, as much of it as a plugin
// has any business seeing. It is a snapshot rather than a set of deltas, so a
// plugin can never act on a half-applied combination.
type State struct {
	// Running reports whether the reader is up.
	Running bool

	// Device is the reader in use, empty when there is none.
	Device string

	// Card is the tag on the reader, if any.
	Card Card

	// Port is the port being served, 0 when nothing is serving.
	Port int

	// TLS reports whether that port is served over TLS, which decides whether
	// the addresses are ws:// or wss://.
	TLS bool

	// Paired counts the devices holding a pairing credential.
	Paired int

	// Settings is what the agent is set to: reader mode, card-type filter,
	// pairing requirement and the rest. Every preference lives here and nowhere
	// else, so a plugin cannot read one value while the agent acts on another.
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
