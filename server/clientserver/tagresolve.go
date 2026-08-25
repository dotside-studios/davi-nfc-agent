package clientserver

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
func (s *tagOps) resolveRoute(uid, deviceID string, allowUntargeted bool) (route, error) {
	// A named device is held to the UID too, when one is given.
	if deviceID != "" {
		rt, ok := s.holding(deviceID)
		if !ok {
			return route{}, refuse(protocol.ErrCodeNoCard, "device %s is not holding a tag", deviceID)
		}
		if uid != "" {
			if rt.uid != "" && !strings.EqualFold(rt.uid, uid) {
				return route{}, refuse(protocol.ErrCodeTagMismatch,
					"request named tag %s but device %s is holding %s", uid, deviceID, rt.uid)
			}
			// The caller named it, so the tag it named is what the source is
			// held to, whether or not the source could name the one it has.
			rt.uid = uid
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

// holding asks what the named device is holding. An empty deviceID asks for
// whatever is holding a tag.
func (s *tagOps) holding(deviceID string) (route, bool) {
	holder := s.tags
	if holder == nil {
		return route{}, false
	}
	device, uid, ok := holder.TagOn(deviceID)
	if !ok {
		return route{}, false
	}
	return route{holder: holder, device: device, uid: uid}, true
}

// holdingUID finds the device holding a tag by UID.
func (s *tagOps) holdingUID(uid string) (route, bool) {
	holder := s.tags
	if holder == nil {
		return route{}, false
	}
	for _, device := range holder.DevicesHoldingTags() {
		held, heldUID, ok := holder.TagOn(device)
		if ok && strings.EqualFold(heldUID, uid) {
			return route{holder: holder, device: held, uid: heldUID}, true
		}
	}
	return route{}, false
}

// guessRoute is what allowUntargeted opts into: whatever is holding a tag.
func (s *tagOps) guessRoute() (route, error) {
	if rt, ok := s.holding(""); ok {
		return rt, nil
	}
	return route{}, refuse(protocol.ErrCodeNoCard, "no reader or device is holding a tag")
}
