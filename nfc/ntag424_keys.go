package nfc

import (
	"bytes"
	"maps"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// NTAG424Keys are the AES-128 keys the agent holds for an NTAG 424 DNA: a
// master key the per-card keys derive from, optionally diversified by UID, and
// any keys held explicitly by number.
//
// A card with the random UID on presents a different 4-byte UID each tap, and
// the real one is only readable after authenticating. With Diversify set the
// authentication key then cannot be derived, so KeySet.Slots must hold an
// undiversified key explicitly (any slot; the lowest is used) for the agent to
// learn the UID. Without one such a card is left unresolved and keys derived
// from its UID are refused.
//
// Keys live in memory for as long as the process does. Nothing persists them
// and nothing logs them.
type NTAG424Keys = ntag424.KeySet

// ntag424KeyConfigurable is implemented by tags that authenticate with AES keys
// the agent holds (the NTAG 424 DNA).
type ntag424KeyConfigurable interface {
	SetNTAG424Keys(keys NTAG424Keys)
}

func ntag424KeysEqual(a, b NTAG424Keys) bool {
	return a.Diversify == b.Diversify &&
		a.AllowLRP == b.AllowLRP &&
		bytes.Equal(a.Master, b.Master) &&
		bytes.Equal(a.SystemID, b.SystemID) &&
		maps.EqualFunc(a.Slots, b.Slots, bytes.Equal)
}
