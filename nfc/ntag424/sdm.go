// Package ntag424 verifies the Secure Dynamic Messaging (SDM, also called SUN)
// data an NTAG 424 DNA mirrors into the URL it serves.
//
// A tag configured for SDM rewrites its own NDEF message on every read: it
// mirrors its UID and a read counter into the URL, optionally encrypted, and
// appends a CMAC over the result. A backend holding the tag's keys can tell a
// genuine tap from a copied URL, and see the counter rise once per tap.
//
// These are pure functions over the values a URL carries. Nothing here touches a
// reader, so a server that never sees an NFC device can verify a tap.
//
// Changing a tag, rather than reading one, needs an authenticated session. See
// Authenticator and Session, which establish and carry one; the transport stays
// the caller's there too.
//
// The algorithms are NXP's AN12196, "NTAG 424 DNA and NTAG 424 DNA TagTamper
// features and hints", and the tests pin every step to the worked examples in
// that document. LRP-mode tags are not supported; only the AES cipher suite is.
package ntag424

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// PICCDataSize is the length of the encrypted PICCData block a tag mirrors.
const PICCDataSize = 16

// Errors reported here, kept distinct because a caller answers them differently:
// a bad MAC is a forgery or the wrong key, a malformed parameter is a request
// that never came from a tag.
var (
	// ErrMACMismatch reports that the MAC did not match the data. The tap is
	// not genuine, or the key is not this tag's.

	// ErrPICCData reports encrypted PICCData that is not one AES block, or
	// that does not decrypt to a well-formed PICCData structure.
	ErrPICCData = errors.New("ntag424: malformed PICCData")
)

// Keys are the two diversified keys a tag's SDM configuration names. They are
// often the same key, and on a factory-fresh tag both are all zero.
type Keys struct {
	// MetaRead is KSDMMetaRead, which encrypts the mirrored PICCData. It is
	// needed only when the tag mirrors PICCData encrypted rather than in the
	// clear.
	MetaRead []byte

	// FileRead is KSDMFileRead, from which the per-tap session keys are
	// derived. It is what the MAC and any encrypted file data depend on.
	FileRead []byte
}

// PICCData is what the tag says about itself on this tap.
type PICCData struct {
	// UID is the tag's 7-byte serial number.
	UID []byte

	// ReadCounter counts reads of the NDEF file, rising by one per tap. A
	// counter at or below one already seen is a replayed URL.
	ReadCounter uint32

	// UIDMirrored and CounterMirrored report which fields the tag mirrored. An
	// unmirrored field is absent rather than zero, and is not part of the
	// session keys.
	UIDMirrored     bool
	CounterMirrored bool
}

// piccDataTag bit assignments, from the PICCDataTag byte that opens a decrypted
// PICCData block.
const (
	piccTagUIDMirrored     = 0x80
	piccTagCounterMirrored = 0x40
	piccTagUIDLengthMask   = 0x0F
)

// uidLength is the only UID length an NTAG 424 DNA reports.
const uidLength = 7

// counterLength is the width of the read counter, carried least significant
// byte first.
const counterLength = 3

// DecryptPICCData recovers the UID and read counter from the encrypted
// PICCData a tag mirrored, using the SDM meta read key.
//
// The block is AES-128-CBC with a zero IV. The result is rejected unless its
// leading tag byte describes the structure that follows, which is all this step
// can check on its own; the MAC is what authenticates a tap.
func DecryptPICCData(metaReadKey, encrypted []byte) (*PICCData, error) {
	block, err := ev2.NewCipher(metaReadKey)
	if err != nil {
		return nil, err
	}
	if len(encrypted) != PICCDataSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrPICCData, len(encrypted), PICCDataSize)
	}

	plain := make([]byte, PICCDataSize)
	cipher.NewCBCDecrypter(block, make([]byte, ev2.BlockSize)).CryptBlocks(plain, encrypted)
	return parsePICCData(plain)
}

// parsePICCData reads a decrypted PICCData block: a tag byte saying which
// fields are present, those fields, then random padding.
func parsePICCData(plain []byte) (*PICCData, error) {
	tag := plain[0]
	data := &PICCData{
		UIDMirrored:     tag&piccTagUIDMirrored != 0,
		CounterMirrored: tag&piccTagCounterMirrored != 0,
	}

	// A block decrypted under the wrong key is random bytes, and shows up here:
	// the length field must be the one length an NTAG 424 DNA has.
	length := int(tag & piccTagUIDLengthMask)
	if data.UIDMirrored && length != uidLength {
		return nil, fmt.Errorf("%w: PICCDataTag %#02x claims a %d-byte UID", ErrPICCData, tag, length)
	}

	offset := 1
	if data.UIDMirrored {
		data.UID = append([]byte(nil), plain[offset:offset+uidLength]...)
		offset += uidLength
	}
	if data.CounterMirrored {
		if offset+counterLength > len(plain) {
			return nil, fmt.Errorf("%w: no room for the read counter", ErrPICCData)
		}
		data.ReadCounter = decodeCounter(plain[offset : offset+counterLength])
	}
	return data, nil
}

// decodeCounter reads the 3-byte read counter, which is least significant byte
// first.
func decodeCounter(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

// encodeCounter writes the counter in the 3-byte form the session vectors use.
func encodeCounter(ctr uint32) []byte {
	return []byte{byte(ctr), byte(ctr >> 8), byte(ctr >> 16)}
}

// Session vector prefixes. The two differ only in their first two bytes, which
// is what separates an encryption key from a MAC key derived from the same
// file read key.
var (
	sv1Prefix = []byte{0xC3, 0x3C, 0x00, 0x01, 0x00, 0x80}
	sv2Prefix = []byte{0x3C, 0xC3, 0x00, 0x01, 0x00, 0x80}
)

// SessionKeys derives this tap's encryption and MAC keys from the file read
// key, the UID and the read counter.
//
// Only the mirrored fields go into the vectors, so a tag that mirrors the
// counter but not the UID derives different keys from one that mirrors both.
func SessionKeys(fileReadKey []byte, data *PICCData) (encKey, macKey []byte, err error) {
	block, err := ev2.NewCipher(fileReadKey)
	if err != nil {
		return nil, nil, err
	}
	return ev2.CMAC(block, sessionVector(sv1Prefix, data)),
		ev2.CMAC(block, sessionVector(sv2Prefix, data)),
		nil
}

// sessionVector builds the 16-byte input the session keys are derived from:
// the prefix, the mirrored fields, then zero padding.
func sessionVector(prefix []byte, data *PICCData) []byte {
	sv := make([]byte, ev2.BlockSize)
	n := copy(sv, prefix)
	if data.UIDMirrored {
		n += copy(sv[n:], data.UID)
	}
	if data.CounterMirrored {
		copy(sv[n:], encodeCounter(data.ReadCounter))
	}
	return sv
}

// MAC returns the SDMMAC over input for this tap: the CMAC under the tap's
// session MAC key, truncated as the tag truncates it.
//
// input is the mirrored file data the tag covered, empty when the tag mirrors
// nothing but PICCData.
func MAC(fileReadKey []byte, data *PICCData, input []byte) ([]byte, error) {
	_, macKey, err := SessionKeys(fileReadKey, data)
	if err != nil {
		return nil, err
	}
	session, err := aes.NewCipher(macKey)
	if err != nil {
		return nil, err
	}
	return ev2.TruncateMAC(ev2.CMAC(session, input)), nil
}

// VerifyMAC reports whether mac is the tag's MAC over input for this tap. The
// comparison is constant time, so verifying an attacker-supplied MAC does not
// leak how much of it was right.
func VerifyMAC(fileReadKey []byte, data *PICCData, input, mac []byte) error {
	want, err := MAC(fileReadKey, data, input)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(want, mac) != 1 {
		return ErrMACMismatch
	}
	return nil
}

// DecryptFileData recovers the file data a tag mirrored encrypted, for a tag
// configured to mirror some of its NDEF file that way.
//
// The IV is the read counter encrypted under the session encryption key, so two
// taps never encrypt the same file data alike.
func DecryptFileData(fileReadKey []byte, data *PICCData, encrypted []byte) ([]byte, error) {
	encKey, _, err := SessionKeys(fileReadKey, data)
	if err != nil {
		return nil, err
	}
	session, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	if len(encrypted) == 0 || len(encrypted)%ev2.BlockSize != 0 {
		return nil, fmt.Errorf("ntag424: encrypted file data is %d bytes, want a multiple of %d", len(encrypted), ev2.BlockSize)
	}

	iv := make([]byte, ev2.BlockSize)
	copy(iv, encodeCounter(data.ReadCounter))
	session.Encrypt(iv, iv)

	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(session, iv).CryptBlocks(plain, encrypted)
	return plain, nil
}

// UIDString renders the UID the way the rest of the agent writes one:
// uppercase hex, no separators.
func (d *PICCData) UIDString() string {
	return strings.ToUpper(hex.EncodeToString(d.UID))
}
