// Package tagrouter answers one question for every client request: which tag
// does this apply to, and is the agent allowing it? It resolves the tag the
// request names against whatever is holding one, applies the agent's policy,
// and reports the outcome in the codes a client understands.
//
// It serves no HTTP. The device protocol belongs to nfc/remotenfc and the
// listener to server/listener; this is the part between a client's request and
// the tag it names.
package tagrouter

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// Config names what holds the tags and what the agent allows to be done to
// them.
type Config struct {
	// Tags answers for every tag the agent can reach, which is the supervisor:
	// the readers it polls and the devices that report their own scans. Nil
	// leaves nothing to route to.
	Tags nfc.TagHolder

	// AllowTagModification reports whether writes, locks and raw exchanges are
	// allowed, which is the agent's mode rather than any one source's. Nil
	// allows them, and the source enforces its own policy either way.
	AllowTagModification func() bool
}

// Router resolves a client request to the tag it names.
type Router struct {
	config Config
}

// New builds the router. It has no lifetime of its own: every operation is a
// call, so there is nothing to start or stop.
func New(config Config) *Router {
	return &Router{config: config}
}

// modificationAllowed reports whether the agent permits a write, a lock or a
// raw exchange. It governs tags held by devices as well as those on a reader:
// the mode is the agent's.
func (s *Router) modificationAllowed() bool {
	return s.config.AllowTagModification == nil || s.config.AllowTagModification()
}

// readOnlyModeMessage explains a mode refusal for the named operation.
func readOnlyModeMessage(operations string) string {
	return fmt.Sprintf("Agent is in read-only mode; %s are refused", operations)
}

// operationErrorCode classifies a reader failure, falling back to the
// operation's own label when the error carries no code of its own.
func operationErrorCode(err error, fallback protocol.ErrorCode) protocol.ErrorCode {
	if payload := protocol.ErrorPayloadFor(err); payload.Code != protocol.ErrCodeUnknownError {
		return payload.Code
	}
	return fallback
}
