// Package e2e exercises the agent as a program embedding it does: built through
// the library's own entry points, served from its real TLS listener, and driven
// from outside over the two WebSocket protocols it publishes.
//
// The wiring in these tests is the wiring in docs/custom-builds.md, deliberately
// -- a driver for phones handed over as an interface, a channel and a handler
// builder, an NFC backend passed to Setup, and nothing reached into afterwards.
// What that documents is what breaks here when it stops being true.
//
// Everything under the tests is the shipped code path. The one stand-in is the
// reader hardware, because that is the single part a test cannot supply.
package e2e
