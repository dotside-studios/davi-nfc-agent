package tagrouter

import (
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// route names where an operation goes: the hardware reader, or one remote
// device. Exactly one of the two is set.
type route struct {
	// reader is set when the agent's own reader should perform the operation.
	// TagUID travels with it so the reader can check, at the moment it has the
	// tag, that the tag present is the one the request named.
	reader bool

	// device is the remote device holding the tag, when one does.
	device deviceTag
}

// refuse explains a refusal in the terms the client sees. The code travels on
// the error itself, so an operation can return one rather than reporting it
// beside a value.
func refuse(code protocol.ErrorCode, format string, args ...any) error {
	return protocol.Errorf(code, format, args...)
}

// resolveRoute decides which tag source an operation applies to, by looking up
// the tag it names rather than preferring one source over another. A preference
// is re-evaluated when the request arrives, so a card lifted since the scan
// would move the request to whichever phone had scanned last.
func (s *Router) resolveRoute(uid, deviceID string, allowUntargeted bool) (route, error) {
	reader := s.config.Reader

	// A named device is held to the UID too, when one is given.
	if deviceID != "" {
		active, ok := s.targetDevice(deviceID)
		if !ok {
			return route{}, refuse(protocol.ErrCodeNoCard, "device %s is not holding a tag", deviceID)
		}
		if uid != "" && !strings.EqualFold(active.UID, uid) {
			return route{}, refuse(protocol.ErrCodeTagMismatch,
				"request named tag %s but device %s is holding %s", uid, deviceID, active.UID)
		}
		return route{device: active}, nil
	}

	if uid != "" {
		if active, ok := s.deviceHoldingUID(uid); ok {
			return route{device: active}, nil
		}
		if readerHoldsUID(reader, uid) {
			return route{reader: true}, nil
		}
		return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding tag %s", uid)
	}

	if !allowUntargeted {
		return route{}, refuse(protocol.ErrCodeTagNotNamed,
			"request must name the tag it applies to (uid), or the device holding it (deviceID)")
	}
	return s.guessRoute(reader)
}

// deviceHoldingUID finds the remote device holding a tag, by UID.
func (s *Router) deviceHoldingUID(uid string) (deviceTag, bool) {
	if s.devices == nil {
		return deviceTag{}, false
	}
	for _, deviceID := range s.devices.DevicesHoldingTags() {
		active, ok := s.targetDevice(deviceID)
		if ok && strings.EqualFold(active.UID, uid) {
			return active, true
		}
	}
	return deviceTag{}, false
}

// readerHoldsUID reports whether the reader's last scan carries uid. This is a
// cached view and only selects the route; the reader re-checks the tag it
// actually holds when it performs the operation.
func readerHoldsUID(reader *nfc.NFCReader, uid string) bool {
	if reader == nil {
		return false
	}
	return strings.EqualFold(reader.GetLastScannedData(), uid)
}

// guessRoute is what allowUntargeted opts into: the reader while it reports a
// card, otherwise the most recent remote scan.
func (s *Router) guessRoute(reader *nfc.NFCReader) (route, error) {
	if reader != nil && reader.GetDeviceStatus().CardPresent {
		return route{reader: true}, nil
	}
	if active, ok := s.targetDevice(""); ok {
		return route{device: active}, nil
	}
	if reader != nil {
		return route{reader: true}, nil
	}
	return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding a tag")
}
