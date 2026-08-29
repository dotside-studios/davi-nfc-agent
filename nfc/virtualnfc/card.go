package virtualnfc

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// Card is a virtual tag ready to be presented on a virtual Device: a UID and the
// nfc.Tag that performs its operations. The tag is the backend — either
// driver-backed over emulated silicon (NewDriverCard) or route-backed
// (NewRoutedCard) — and everything above the Device sees only an ordinary
// nfc.Tag, whichever it is.
type Card struct {
	uid string
	tag nfc.Tag
}

// NewCard wraps any nfc.Tag as a presentable card. Prefer NewDriverCard or
// NewRoutedCard; this is the escape hatch for a tag built elsewhere.
func NewCard(uid string, tag nfc.Tag) *Card {
	return &Card{uid: uid, tag: tag}
}

// NewDriverCard wraps an nfc.CardTransport in the production tag driver for the
// given kind, so real driver I/O (page/block/APDU logic, TLV, lock bytes) runs
// against the transport. The transport is the caller's emulated silicon; this
// package holds none of its own.
func NewDriverCard(transport nfc.CardTransport, uid string, kind nfc.DetectedTagType) *Card {
	return &Card{uid: uid, tag: nfc.NewEmulatedTag(transport, uid, kind)}
}

// NewRoutedCard builds a card backed by a RoutedTag, for a tag whose operations
// happen elsewhere.
func NewRoutedCard(cfg RoutedTagConfig) *Card {
	return &Card{uid: cfg.UID, tag: NewRoutedTag(cfg)}
}

// UID returns the card's UID.
func (c *Card) UID() string { return c.uid }

// Tag returns the underlying nfc.Tag.
func (c *Card) Tag() nfc.Tag { return c.tag }
