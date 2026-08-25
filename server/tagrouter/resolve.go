package tagrouter

import (
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// route names where an operation goes: one of the agent's own readers, or one
// remote device. Exactly one of the two is set.
type route struct {
	// reader names the reader that should perform the operation, empty when a
	// remote device holds the tag. TagUID travels separately, so the reader can
	// check at the moment it has the tag that the one present is the one the
	// request named.
	reader string

	// device is the remote device holding the tag, when one does.
	device deviceTag
}

// onReader reports whether the operation goes to one of the agent's readers.
func (r route) onReader() bool { return r.reader != "" }

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
	readers := s.config.Readers

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
		if device, ok := readerHolding(readers, uid); ok {
			return route{reader: device}, nil
		}
		return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding tag %s", uid)
	}

	if !allowUntargeted {
		return route{}, refuse(protocol.ErrCodeTagNotNamed,
			"request must name the tag it applies to (uid), or the device holding it (deviceID)")
	}
	return s.guessRoute(readers)
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

// readerHolding names the reader whose last scan carries uid, when one does.
func readerHolding(readers *nfc.Supervisor, uid string) (string, bool) {
	if readers == nil {
		return "", false
	}
	return readers.Holding(uid)
}

// guessRoute is what allowUntargeted opts into: a reader with a card on it,
// otherwise the most recent remote scan, otherwise a reader to try.
func (s *Router) guessRoute(readers *nfc.Supervisor) (route, error) {
	if readers != nil {
		if device, ok := readers.Present(); ok {
			return route{reader: device}, nil
		}
	}
	if active, ok := s.targetDevice(""); ok {
		return route{device: active}, nil
	}
	if readers != nil {
		if device, ok := readers.Any(); ok {
			return route{reader: device}, nil
		}
	}
	return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding a tag")
}
