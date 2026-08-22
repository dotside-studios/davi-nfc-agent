package plugin

import "net/http"

// Route is one path a plugin serves on the agent's listener.
//
// The pattern is an http.ServeMux one, so a trailing slash takes the whole
// subtree. Whatever is serving the agent's port mounts these; a plugin with a
// page of its own therefore needs no listener, no port and no certificate of
// its own, and is reachable at the address a device already trusts.
type Route struct {
	Pattern string
	Handler http.Handler

	// Owner is the ID of the plugin that asked for it. It is filled in by the
	// host as the routes are collected, so a route that cannot be served names
	// where to go and fix it.
	Owner string
}

// RouteProvider is a plugin with something to serve over HTTP. It is asked for
// its routes as the listener comes up, so a plugin registered before the server
// is mounted by it all the same.
type RouteProvider interface {
	Routes() []Route
}
