package deviceserver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
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
	device remotenfc.ActiveTagInfo
}

// routeError explains a refusal in the terms the client sees.
type routeError struct {
	code protocol.ErrorCode
	msg  string
}

func (e *routeError) Error() string { return e.msg }

func refuse(code protocol.ErrorCode, format string, args ...any) *routeError {
	return &routeError{code: code, msg: fmt.Sprintf(format, args...)}
}

// resolveRoute decides which tag source an operation applies to, by looking up
// the tag it names rather than preferring one source over another. A preference
// is re-evaluated when the request arrives, so a card lifted since the scan
// used to move the request to whichever phone had scanned last.
func (s *Server) resolveRoute(uid, deviceID string, allowUntargeted bool) (route, error) {
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
func (s *Server) deviceHoldingUID(uid string) (remotenfc.ActiveTagInfo, bool) {
	if s.remote == nil {
		return remotenfc.ActiveTagInfo{}, false
	}
	for _, deviceID := range s.remote.ActiveTagDevices() {
		active, ok := s.remote.ActiveTag(deviceID)
		if ok && strings.EqualFold(active.UID, uid) {
			return active, true
		}
	}
	return remotenfc.ActiveTagInfo{}, false
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
func (s *Server) guessRoute(reader *nfc.NFCReader) (route, error) {
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

// routeFailure unwraps a refusal into the code and message a client receives.
func routeFailure(err error) (protocol.ErrorCode, string) {
	var re *routeError
	if errors.As(err, &re) {
		return re.code, re.msg
	}
	return protocol.ErrCodeNoCard, err.Error()
}
