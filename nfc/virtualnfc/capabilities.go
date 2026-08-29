package virtualnfc

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// CapabilitySource reports what a backend can carry, independent of any one tag.
// A Route is the source for a routed tag; the bounds it reports are the device's
// own claims ("this bridge cannot lock anything it holds").
type CapabilitySource interface {
	CanWrite() bool
	CanLock() bool
	CanTransceive() bool
}

// MergeCapabilities combines what a tag declared about itself with what its
// backend can route, producing the nfc.TagCapabilities the tag reports.
//
// The declaration is three-valued: a nil declared means the source said nothing
// about the tag, which is unknown, not incapable — so an undeclared operation
// defers to the source. A declared operation that is false vetoes it, however
// capable the source is. A tag declared IsReadOnly refuses write and lock
// regardless.
//
// Reads are treated as a snapshot: a routed tag answers with what was captured
// when it was scanned, so a write cannot be confirmed by reading it back.
func MergeCapabilities(declared *nfc.TagCapabilities, src CapabilitySource, tagType, technology string, hasNDEF bool) nfc.TagCapabilities {
	var caps nfc.TagCapabilities
	if declared != nil {
		caps = *declared
	}

	if caps.TagFamily == "" {
		caps.TagFamily = tagType
	}
	if caps.Technology == "" {
		caps.Technology = technology
	}
	if !caps.SupportsNDEF {
		caps.SupportsNDEF = hasNDEF
	}

	caps.CanRead = true
	caps.ReadsAreSnapshot = true
	caps.CanWrite = WriteAllowed(declared, src)
	caps.CanLock = LockAllowed(declared, src)
	caps.CanTransceive = TransceiveAllowed(declared, src)
	return caps
}

// WriteAllowed reports whether a write would reach the tag: not read-only, the
// source can route it, and the tag did not declare it cannot.
func WriteAllowed(declared *nfc.TagCapabilities, src CapabilitySource) bool {
	if src == nil || readOnlyDeclared(declared) {
		return false
	}
	return declaredFor(declared, func(c nfc.TagCapabilities) bool { return c.CanWrite }, src.CanWrite())
}

// LockAllowed reports whether the tag can be locked through its source.
func LockAllowed(declared *nfc.TagCapabilities, src CapabilitySource) bool {
	if src == nil || readOnlyDeclared(declared) {
		return false
	}
	return declaredFor(declared, func(c nfc.TagCapabilities) bool { return c.CanLock }, src.CanLock())
}

// TransceiveAllowed reports whether a raw exchange would reach the tag.
func TransceiveAllowed(declared *nfc.TagCapabilities, src CapabilitySource) bool {
	if src == nil {
		return false
	}
	return declaredFor(declared, func(c nfc.TagCapabilities) bool { return c.CanTransceive }, src.CanTransceive())
}

// declaredFor answers one capability from the two claims that bear on it. A tag
// that declared it cannot is refused; otherwise the source decides. A device
// that declared nothing (nil) no longer reads as one that refused.
func declaredFor(declared *nfc.TagCapabilities, ofTag func(nfc.TagCapabilities) bool, sourceCan bool) bool {
	if declared != nil && !ofTag(*declared) {
		return false
	}
	return sourceCan
}

func readOnlyDeclared(declared *nfc.TagCapabilities) bool {
	return declared != nil && declared.IsReadOnly
}
