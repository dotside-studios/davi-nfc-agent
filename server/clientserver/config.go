package clientserver

import (
	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Config holds configuration for the client handling logic. The HTTP listener
// and TLS are owned by the unified server, so this carries only what the client
// handlers need.
type Config struct {
	// APISecret is the secret required on every connection, read on each one
	// so a rotation takes effect without rebuilding the server. Nil, or one
	// returning empty, requires no secret, which is the development default.
	APISecret func() string

	// AllowLoopbackBypass reports whether a connection from the host itself
	// may skip the secret, read per connection so the policy can change under
	// a running server. Nil requires the secret from loopback like anywhere
	// else; see [server.AuthOptions.AllowLoopback] for why that is the
	// default.
	AllowLoopbackBypass func() bool

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

	// Scans carries every tag to broadcast, and ReaderStatus every change in a
	// reader's status. The server connects to both when it is built and
	// disconnects in Close, so a server that has been replaced stops receiving
	// rather than broadcasting to clients nothing is reading.
	//
	// Nil for a server fed by hand through Broadcast and BroadcastDeviceStatus.
	Scans        *event.Signal[nfc.NFCData]
	ReaderStatus *event.Signal[nfc.DeviceStatus]
}
