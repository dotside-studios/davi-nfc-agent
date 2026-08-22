package wsserver

import "net/http"

// Route is one path a plugin serves on this listener.
//
// The pattern is an http.ServeMux one, so a trailing slash takes the whole
// subtree. A plugin with a page of its own therefore needs no listener, no port
// and no certificate of its own, and is reachable at the address a device
// already trusts.
type Route struct {
	Pattern string
	Handler http.Handler

	// Label lists this route's address on the server's own menu, under this
	// name, beside the addresses of the endpoints it serves itself. Leave it
	// empty for a route nobody is meant to be handed — the control center is
	// opened with a token rather than a URL, so it has none.
	//
	// The address itself is not the plugin's to build: this server knows the
	// scheme, the host and the port it bound, and none of the three are the
	// plugin's to guess at.
	Label string

	// Tooltip is the hover text on that entry.
	Tooltip string

	// Owner is the ID of the plugin that asked for the route, filled in as the
	// routes are collected so that one which cannot be served names where to go
	// and fix it.
	Owner string
}

// RouteProvider is a plugin with something to serve over HTTP. Every registered
// plugin implementing it is asked for its routes as the listener comes up, so
// one registered after this server is mounted by it all the same.
type RouteProvider interface {
	Routes() []Route
}
