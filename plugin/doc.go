// Package plugin is what the agent is assembled from.
//
// The agent proper is a reader, its settings and the stores behind them.
// Everything that serves something is a plugin: the WebSocket servers a device
// and a web page connect to, the pairing server a phone pairs against, and
// whatever a consumer builds on top — a turnstile, a kiosk, a badge desk. A
// build that wants none of them leaves them out, and the agent still reads
// cards.
//
//	host := plugin.New(plugin.Config{Logf: log.Printf, Menus: tray})
//	host.Use(
//	    wsserver.New(wsserver.Config{Agent: agent}),
//	    pairing.New(pairing.Config{Server: bootstrap, Port: 9472}),
//	    &turnstile{gate: gate},
//	)
//
//	host.Init()   // wire up: menus, peers
//	host.Start()  // begin serving
//	defer host.Close()
//
// # Lifecycle
//
// A plugin declares only the phases it has work in. Nothing beyond [Info] is
// required, so a plugin that just adds a menu implements one method and one
// phase:
//
//	Init    once, before anything serves: fill a menu, find a peer
//	Start   begin serving; called again after a Restart
//	Stop    stop serving, the inverse of Start
//	Close   release what outlives serving, on the way out
//
// The order is registration order for Init and Start, and the reverse of it for
// Stop and Close, so a plugin registered after the thing it depends on is
// started after it and stopped before it.
//
// # What a plugin gets
//
// A [Context] of its own: a menu it fills, a snapshot of what the agent is
// doing and a signal raised whenever that changes, its peers, the clipboard, a
// browser, and the log.
//
// That is all of it. This package has no notion of an address, an HTTP route or
// anything else a particular plugin does — a plugin that serves something draws
// its own menu for it, and the agent's own features get no seam a consumer's
// cannot reach.
//
// Nothing in it names the tray library beyond the container a menu is filled
// through, and nothing names fyne.io/systray. A build with no tray leaves
// [Config.Menus] nil and a plugin that fills a menu still runs.
//
// # Reaching other plugins
//
// By capability rather than by name, with [Find] and [FindAll]. It is how the
// agent asks what is serving it, and how a server gathers the pages its peers
// want it to mount:
//
//	for _, provider := range plugin.FindAll[wsserver.RouteProvider](host) { ... }
//
// # Registration
//
// Go has no portable dynamic loading, so a plugin is compiled in. A consumer
// building on this agent registers theirs from an init function in their own
// package and changes nothing in the agent:
//
//	func init() { plugin.Register(&turnstile{gate: OpenGate()}) }
//
// The command line takes up the default registry at startup, alongside the
// plugins the agent ships with.
package plugin
