// Package console serves the agent's control center: a privileged HTTP API
// under /control and the page that drives it, whose source lives in frontend/
// and whose build is embedded by embed.go.
//
// The page reaches the agent through Host, implemented for a live agent by
// host.go. Everything the console needs is declared on that interface, so the
// serving half can be exercised against a fake.
//
// Under -tags nowebui the console is absent: New returns nil, every method
// tolerates a nil receiver, and no frontend is embedded.
package console
