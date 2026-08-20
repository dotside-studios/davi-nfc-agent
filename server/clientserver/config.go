package clientserver

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Config holds configuration for the client handling logic. The HTTP listener
// and TLS are owned by the unified server, so this carries only what the client
// handlers need.
type Config struct {
	// APISecret is the API secret required for non-loopback connections.
	// Empty means no auth (legacy / development mode).
	APISecret string

	// AllowedOrigins extends the default same-origin policy. Use ["*"]
	// to disable origin checking entirely (NOT recommended).
	AllowedOrigins []string

	// OriginPolicy, when set, decides origin admission instead of
	// AllowedOrigins and is told about rejections.
	OriginPolicy server.OriginPolicy

	// TokenVerifier recognizes per-device credentials issued at pairing, so a
	// device can be admitted -- and revoked -- independently of the shared
	// secret.
	TokenVerifier server.TokenVerifier

	// Ops performs the tag operations a client asks for. Nil refuses them,
	// which is what an agent that is not running does.
	Ops server.TagOps

	// OnChange, when set, is called whenever a client connects or disconnects
	// so an observer can refresh without polling. Called off the hot path but
	// on the connection's own goroutine, so it must not block.
	OnChange func()

	// OnTag, when set, is called for every scan before it is broadcast, so a
	// program embedding the agent can act on cards without pretending to be a
	// WebSocket client. It observes rather than intercepts: the scan is
	// broadcast either way, and returning changes nothing.
	//
	// Called on the goroutine draining the bridge, so it must not block --
	// that goroutine also feeds every connected client.
	OnTag func(nfc.NFCData)
}
