package ntag424

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// maxDiversificationInput is the longest message AN10922 allows after the
// 0x01 constant: one 32-byte block.
const maxDiversificationInput = 32

// DiversifyKey derives a per-card AES-128 key from a master key, as NXP's
// AN10922 section 2.2.1 specifies.
//
// The input is the constant 0x01, the UID, then systemID, which names the
// application so two applications on one card get different keys. It is
// CMACed under the master key; a message shorter than 32 bytes is padded with
// 0x80 and zeros and uses the second subkey, which is what the CMAC does for
// any short message.
func DiversifyKey(masterKey, uid, systemID []byte) ([]byte, error) {
	block, err := ev2.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	if len(uid) == 0 {
		return nil, fmt.Errorf("ntag424: diversification needs the card's UID")
	}
	input := make([]byte, 0, maxDiversificationInput)
	input = append(input, 0x01)
	input = append(input, uid...)
	input = append(input, systemID...)
	if len(input) > maxDiversificationInput {
		return nil, fmt.Errorf("ntag424: diversification input is %d bytes, at most %d", len(input), maxDiversificationInput)
	}
	return ev2.CMAC(block, input), nil
}

// KeySet is the keys held for one kind of tag: a master key the per-card keys
// derive from, and any keys held explicitly.
type KeySet struct {
	// Master is the key for slots with no explicit entry, used as it is or
	// diversified by UID.
	Master []byte

	// SystemID is the application identifier folded into diversification.
	SystemID []byte

	// Slots holds keys by key number. An entry here wins over Master.
	Slots map[byte][]byte

	// Diversify derives each slot's key from Master and the card's UID rather
	// than using Master itself.
	Diversify bool
}

// Key returns the key for a key number on the card with this UID. The second
// result is false when this set holds no key for it.
func (k KeySet) Key(keyNo byte, uid []byte) ([]byte, bool) {
	if key, ok := k.Slots[keyNo]; ok {
		if len(key) != KeySize {
			return nil, false
		}
		return append([]byte(nil), key...), true
	}
	if keyNo > 4 || len(k.Master) != KeySize {
		return nil, false
	}
	if !k.Diversify {
		return append([]byte(nil), k.Master...), true
	}
	key, err := DiversifyKey(k.Master, uid, k.SystemID)
	if err != nil {
		return nil, false
	}
	return key, true
}

// Copy returns a set that shares no memory with k.
func (k KeySet) Copy() KeySet {
	out := KeySet{
		Master:    append([]byte(nil), k.Master...),
		SystemID:  append([]byte(nil), k.SystemID...),
		Diversify: k.Diversify,
	}
	if k.Master == nil {
		out.Master = nil
	}
	if k.SystemID == nil {
		out.SystemID = nil
	}
	if k.Slots != nil {
		out.Slots = make(map[byte][]byte, len(k.Slots))
		for n, key := range k.Slots {
			out.Slots[n] = append([]byte(nil), key...)
		}
	}
	return out
}

// Empty reports whether the set holds no keys at all.
func (k KeySet) Empty() bool {
	return len(k.Master) == 0 && len(k.Slots) == 0
}
