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
	// device can be admitted, and revoked, independently of the shared
	// secret.
	TokenVerifier server.TokenVerifier

	// Tags answers for every tag the agent can reach, which is the supervisor:
	// the readers it polls and the devices that report their own scans. The
	// server resolves the tag a request names against it.
	Tags nfc.TagHolder

	// AllowTagModification reports whether writes, locks and raw exchanges are
	// allowed, which is the agent's mode rather than any one source's. Nil
	// allows them, and the source enforces its own policy either way.
	AllowTagModification func() bool

	// Ops replaces the operations the server would perform over Tags, for a
	// build that has its own. Nil uses Tags; with neither, operations are
	// refused, which is what an agent that is not running does.
	Ops server.TagOps

	// OnChange, when set, is called with the number of connected clients
	// whenever one connects or disconnects, so an observer can refresh without
	// polling. Called off the hot path but on the connection's own goroutine,
	// so it must not block.
	OnChange func(clients int)
}
