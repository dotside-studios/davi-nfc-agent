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

	// Label puts this route's address on the agent's menus, under this name,
	// beside the agent's own. Leave it empty for a route nobody is meant to be
	// handed — the control center is opened with a token rather than a URL, so
	// it has no label.
	//
	// The address itself is not the plugin's to build. Whatever mounts the
	// route knows the scheme, the host and the port it bound, and publishes an
	// [Endpoint] from those and the pattern.
	Label string

	// Tooltip is the hover text on that entry.
	Tooltip string

	// Owner is the ID of the plugin that asked for it. It is filled in by the
	// host as the routes are collected, so a route that cannot be served names
	// where to go and fix it.
	Owner string
}

// EndpointID is what a labelled route's address is registered under. It is
// derived rather than chosen, so a plugin serving two labelled paths gets two
// entries without having to key them itself.
func (r Route) EndpointID() string { return "route:" + r.Owner + r.Pattern }

// RouteProvider is a plugin with something to serve over HTTP. It is asked for
// its routes as the listener comes up, so a plugin registered before the server
// is mounted by it all the same.
type RouteProvider interface {
	Routes() []Route
}
