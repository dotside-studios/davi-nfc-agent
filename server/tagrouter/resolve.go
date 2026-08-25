package tagrouter

import (
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// route names what an operation goes to: the source holding the tag, which
// device of it, and the tag's UID.
//
// The UID travels separately from the request's, so whatever performs the
// operation can check at the moment it has the tag that the one present is the
// one the request named.
type route struct {
	holder nfc.TagHolder
	device string
	uid    string
}

// refuse explains a refusal in the terms the client sees. The code travels on
// the error itself, so an operation can return one rather than reporting it
// beside a value.
func refuse(code protocol.ErrorCode, format string, args ...any) error {
	return protocol.Errorf(code, format, args...)
}

// resolveRoute decides which source an operation applies to, by looking up the
// tag it names rather than preferring one source over another. A preference is
// re-evaluated when the request arrives, so a card lifted since the scan would
// move the request to whichever source is holding it now.
func (s *Router) resolveRoute(uid, deviceID string, allowUntargeted bool) (route, error) {
	// A named device is held to the UID too, when one is given.
	if deviceID != "" {
		rt, ok := s.holding(deviceID)
		if !ok {
			return route{}, refuse(protocol.ErrCodeNoCard, "device %s is not holding a tag", deviceID)
		}
		if uid != "" && !strings.EqualFold(rt.uid, uid) {
			return route{}, refuse(protocol.ErrCodeTagMismatch,
				"request named tag %s but device %s is holding %s", uid, deviceID, rt.uid)
		}
		return rt, nil
	}

	if uid != "" {
		if rt, ok := s.holdingUID(uid); ok {
			return rt, nil
		}
		return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding tag %s", uid)
	}

	if !allowUntargeted {
		return route{}, refuse(protocol.ErrCodeTagNotNamed,
			"request must name the tag it applies to (uid), or the device holding it (deviceID)")
	}
	return s.guessRoute()
}

// holding asks each source for the tag a device is holding. An empty deviceID
// asks each for its most recent.
func (s *Router) holding(deviceID string) (route, bool) {
	for _, holder := range s.holders() {
		if device, uid, ok := holder.TagOn(deviceID); ok {
			return route{holder: holder, device: device, uid: uid}, true
		}
	}
	return route{}, false
}

// holdingUID finds the source holding a tag by UID.
func (s *Router) holdingUID(uid string) (route, bool) {
	for _, holder := range s.holders() {
		for _, device := range holder.DevicesHoldingTags() {
			held, heldUID, ok := holder.TagOn(device)
			if ok && strings.EqualFold(heldUID, uid) {
				return route{holder: holder, device: held, uid: heldUID}, true
			}
		}
	}
	return route{}, false
}

// guessRoute is what allowUntargeted opts into: whichever source is holding a
// tag, and failing that a reader to try, so a request that has to go somewhere
// is not refused for having no route.
func (s *Router) guessRoute() (route, error) {
	if rt, ok := s.holding(""); ok {
		return rt, nil
	}
	if readers := s.config.Readers; readers != nil {
		if device, ok := readers.Any(); ok {
			return route{holder: readers, device: device}, nil
		}
	}
	return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding a tag")
}

// holders lists the sources to ask, readers first: an operation for a tag on a
// reader should not wait on a phone that is holding one too.
func (s *Router) holders() []nfc.TagHolder {
	var out []nfc.TagHolder
	if s.config.Readers != nil {
		out = append(out, s.config.Readers)
	}
	if s.devices != nil {
		out = append(out, s.devices)
	}
	return out
}
