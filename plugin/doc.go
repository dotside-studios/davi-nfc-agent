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
//	host.Init()   // wire up: menus, addresses, routes, peers
//	host.Start()  // begin serving
//	defer host.Close()
//
// # Lifecycle
//
// A plugin declares only the phases it has work in. Nothing beyond [Info] is
// required, so a plugin that just adds a menu implements one method and one
// phase:
//
//	Init    once, before anything serves: fill a menu, declare an address,
//	        publish routes, find a peer
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
// A [Context] of its own: a menu it fills, the register of addresses the agent
// hands out, a snapshot of what the agent is doing and a signal raised whenever
// that changes, its peers, the clipboard, a browser, and the log.
//
// Nothing in it names the tray library beyond the container a menu is filled
// through, and nothing names fyne.io/systray. A build with no tray leaves
// [Config.Menus] nil and a plugin that fills a menu still runs.
//
// # Serving HTTP
//
// A plugin with a page or an API of its own does not open a listener for it. It
// implements [RouteProvider], and whatever is serving the agent's port mounts
// what it returns:
//
//	func (t *turnstile) Routes() []plugin.Route {
//	    return []plugin.Route{{Pattern: "/turnstile/", Handler: t.mux}}
//	}
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
