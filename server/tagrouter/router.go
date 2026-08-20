// Package tagrouter answers one question for every client request: which of the
// agent's tag sources does this apply to, the hardware reader or a paired
// device? It reads the request channels of the bridge and performs the
// operation on whichever source holds the tag the request names.
//
// It serves no HTTP. The device protocol belongs to nfc/remotenfc and the
// listener to server/unifiedserver; this is the part that has to see both a
// reader and a device driver at once, which is why it is neither of them.
package tagrouter

import (
	"fmt"
	"sync/atomic"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Config names the tag sources to route between.
type Config struct {
	// Reader is the agent's own hardware reader. Nil when it has none.
	Reader *nfc.NFCReader

	// Devices is the driver serving paired devices. Nil when none are
	// configured.
	Devices server.DeviceOps
}

// Router routes client requests to a tag source.
type Router struct {
	config  Config
	devices server.DeviceOps

	// seq labels each request to a device, which correlates its reply by it.
	seq atomic.Uint64
}

// New builds the router. It has no lifetime of its own: every operation is a
// call, so there is nothing to start or stop.
func New(config Config) *Router {
	return &Router{config: config, devices: config.Devices}
}

// targetDevice resolves which remote device a request is for. A request naming
// one is answered by that device or not at all; naming none falls back to the
// most recent scan.
func (s *Router) targetDevice(target string) (deviceTag, bool) {
	if s.devices == nil {
		return deviceTag{}, false
	}
	deviceID, tag, ok := s.devices.TagOn(target)
	if !ok {
		return deviceTag{}, false
	}
	return deviceTag{DeviceID: deviceID, UID: tag.UID(), Tag: tag}, true
}

// deviceTag is a tag a remote device is holding.
type deviceTag struct {
	DeviceID string
	UID      string
	Tag      nfc.Tag
}

// modeAllowsTagModification reports whether the agent's current mode permits a
// write, a lock or a raw exchange.
//
// The mode belongs to the agent rather than to the reader, so it governs tags
// held by remote devices too. A nil reader has no mode to consult.
func modeAllowsTagModification(reader *nfc.NFCReader) bool {
	return reader == nil || reader.GetMode() != nfc.ModeReadOnly
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
