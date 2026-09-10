package ev2

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
)

// KeySize is the length of every key in this protocol: AES-128.
const KeySize = 16

// MACSize is the length of a MAC on the wire, which is a CMAC truncated to half
// its length.
const MACSize = 8

var (
	// ErrMACMismatch reports that a MAC did not match the data it covers. The
	// message is not from the card, or not the one the card sent.
	ErrMACMismatch = errors.New("ev2: MAC does not match")

	// ErrKeySize reports a key that is not AES-128.
	ErrKeySize = errors.New("ev2: key must be 16 bytes")
)

// TruncateMAC keeps the odd-indexed bytes of a full CMAC, which is how the card
// shortens one to eight bytes.
func TruncateMAC(full []byte) []byte {
	out := make([]byte, 0, MACSize)
	for i := 1; i < len(full); i += 2 {
		out = append(out, full[i])
	}
	return out
}

// NewCipher builds the AES block cipher for a key, refusing one of the wrong
// size rather than letting AES report it in its own words.
func NewCipher(key []byte) (cipher.Block, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w, got %d", ErrKeySize, len(key))
	}
	return aes.NewCipher(key)
}
