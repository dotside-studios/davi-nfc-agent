package nfc

// DESFireKeys are the AES-128 keys the agent holds for a DESFire's NDEF
// application, by the key number the card knows each one as.
//
// A DESFire file names the key that may read it and the key that may write it,
// as a number rather than a value. Holding the matching key is what lets the
// agent open a file whose access rights are not free; holding none is the
// default, and such a file reports itself read-only or unreadable instead.
//
// Keys live in memory for as long as the process does. Nothing persists them to
// settings and nothing logs them, which is the same treatment the MIFARE
// Classic keys get. Supply them from the embedding program.
type DESFireKeys map[byte][]byte

// Copy returns a deep copy, so a caller cannot mutate keys the agent is using
// and the agent cannot mutate the caller's.
func (k DESFireKeys) Copy() DESFireKeys {
	if len(k) == 0 {
		return nil
	}
	out := make(DESFireKeys, len(k))
	for no, key := range k {
		out[no] = append([]byte(nil), key...)
	}
	return out
}

// desfireKeyConfigurable is implemented by tags that authenticate with AES keys
// the agent holds (currently DESFire).
type desfireKeyConfigurable interface {
	SetDESFireKeys(keys DESFireKeys)
}
