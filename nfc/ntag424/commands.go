package ntag424

import (
	"fmt"
	"hash/crc32"
)

// The commands that change a tag, built for a session to carry.
//
// These build APDUs and read answers. Nothing here sends anything, and nothing
// in the agent calls them: the tag operations, the client protocol and the
// console have no route to a keyed command, and this package holds no keys.
// Whoever holds a key builds the command, sends it over a channel of their
// choosing, and lives with the result.
//
// That matters most for ChangeKey. A key change cannot be undone, and a wrong
// one leaves a tag nobody can authenticate to. There is no recovery, on any
// key, at any time.

// Instruction bytes for the commands below.
const (
	insChangeFileSettings = 0x5F
	insChangeKey          = 0xC4
	insGetCardUID         = 0x51
	insGetFileSettings    = 0xF5
)

// NDEFFileNo is the file number of the NDEF file, which is the one SDM is
// configured on.
const NDEFFileNo = 0x02

// ChangeKey builds the command that replaces one of the card's AES keys.
//
// The card is told the change in one of two forms, and which one depends on
// whether the key being changed is the one this session authenticated with. A
// key that is not the session's is sent as its difference from the old key,
// with a checksum, so the card can confirm the caller knew the old key; the
// session's own key is sent directly, because authenticating with it already
// proved that.
//
// oldKey is ignored, and may be nil, when the key being changed is the
// session's own. version is the new key's version byte, which the card reports
// afterwards and which is the only way to tell one key generation from another.
//
// This cannot be undone. A card whose key is changed to a value nobody holds is
// finished: there is no recovery path, no reset, and no way back to the factory
// key.
func ChangeKey(s *Session, keyNo, authKeyNo byte, oldKey, newKey []byte, version byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: ChangeKey needs an authenticated session")
	}
	if len(newKey) != KeySize {
		return nil, fmt.Errorf("%w: new key is %d bytes", ErrKeySize, len(newKey))
	}

	var data []byte
	if keyNo == authKeyNo {
		// The session authenticated with this key, so the card already knows
		// the caller holds it. New key and version, nothing else.
		data = append(append([]byte(nil), newKey...), version)
	} else {
		if len(oldKey) != KeySize {
			return nil, fmt.Errorf("%w: changing a key other than the session's needs the old key, got %d bytes", ErrKeySize, len(oldKey))
		}
		// The card holds the old key and can undo the difference. The checksum
		// over the new key is what proves the caller got the right one out.
		data = make([]byte, 0, KeySize+5)
		for i := range newKey {
			data = append(data, oldKey[i]^newKey[i])
		}
		data = append(data, version)
		data = append(data, keyCRC(newKey)...)
	}

	return s.Command(insChangeKey, []byte{keyNo}, data, CommFull)
}

// keyCRC is the checksum a key change carries, least significant byte first.
//
// It is CRC-32 as the card computes it: the same polynomial as the usual one,
// but without the final inversion, which is the convention this family of cards
// uses.
func keyCRC(key []byte) []byte {
	sum := ^crc32.ChecksumIEEE(key)
	return []byte{byte(sum), byte(sum >> 8), byte(sum >> 16), byte(sum >> 24)}
}

// ChangeFileSettings builds the command that rewrites a file's settings, which
// is how SDM is turned on and configured.
//
// settings is the encoded setting block; see FileSettings, whose Encode builds
// one. A caller that has its own encoding can pass it here directly.
func ChangeFileSettings(s *Session, fileNo byte, settings []byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: ChangeFileSettings needs an authenticated session")
	}
	if len(settings) == 0 {
		return nil, fmt.Errorf("ntag424: no file settings to write")
	}
	return s.Command(insChangeFileSettings, []byte{fileNo}, settings, CommFull)
}

// GetFileSettings builds the command that reads a file's settings back. The
// answer is the encoded block, which ParseFileSettings reads.
func GetFileSettings(s *Session, fileNo byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: GetFileSettings needs an authenticated session")
	}
	return s.Command(insGetFileSettings, []byte{fileNo}, nil, CommMAC)
}

// GetCardUID builds the command that reads the card's real UID.
//
// A card configured to answer with a random ID gives a different one on every
// tap, and this is then the only way to learn which card it is.
func GetCardUID(s *Session) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: GetCardUID needs an authenticated session")
	}
	return s.Command(insGetCardUID, nil, nil, CommFull)
}

// ParseCardUID reads the card's answer to GetCardUID.
func ParseCardUID(s *Session, response []byte) ([]byte, error) {
	data, err := s.Response(response, CommFull)
	if err != nil {
		return nil, err
	}
	if len(data) != uidLength {
		return nil, fmt.Errorf("ntag424: card returned a %d-byte UID, want %d", len(data), uidLength)
	}
	return data, nil
}
