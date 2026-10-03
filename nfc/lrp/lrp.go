package lrp

import (
	"crypto/aes"
	"errors"
	"fmt"
)

// KeySize is the length of an LRP key, which is an AES-128 key.
const KeySize = 16

// BlockSize is the AES block size, which LRP's blocks, MACs and counters build
// on.
const BlockSize = aes.BlockSize

// ErrKeySize reports a key that is not 16 bytes.
var ErrKeySize = errors.New("lrp: key must be 16 bytes")

// Which updated key a [Key] evaluates under. A bare key, and the MAC half of a
// session, use the first; the encryption half of a session uses the second.
const (
	UpdateMAC = 0
	UpdateENC = 1
)

// updatedKeyCount is how many updated keys a key is expanded into.
const updatedKeyCount = 3

// Key is a key expanded for LRP: its sixteen plaintexts, and the one updated
// key a use of it evaluates under. A Key is immutable and safe for concurrent
// use.
type Key struct {
	plaintexts [16][BlockSize]byte
	updated    [BlockSize]byte
}

// New expands a key. update picks the updated key the Key evaluates under,
// [UpdateMAC] or [UpdateENC].
func New(key []byte, update int) (*Key, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w, got %d", ErrKeySize, len(key))
	}
	if update < 0 || update >= updatedKeyCount {
		return nil, fmt.Errorf("lrp: updated key %d does not exist", update)
	}
	k := &Key{}
	for i, p := range Plaintexts(key) {
		copy(k.plaintexts[i][:], p)
	}
	copy(k.updated[:], UpdatedKeys(key)[update])
	return k, nil
}

// Plaintexts generates the sixteen plaintexts a key expands into, one per
// nibble value.
//
// The key is first hardened by encrypting a block of 0x55 under it. Each
// plaintext is a block of 0xAA encrypted under the running hardened key, which
// then advances by encrypting 0x55 again.
func Plaintexts(key []byte) [][]byte {
	h := encrypt(key, constantBlock(0x55))
	out := make([][]byte, 16)
	for i := range out {
		out[i] = encrypt(h, constantBlock(0xAA))
		h = encrypt(h, constantBlock(0x55))
	}
	return out
}

// UpdatedKeys generates the updated keys a key expands into. The first starts
// the evaluation of a MAC, the second of an encryption.
func UpdatedKeys(key []byte) [][]byte {
	h := encrypt(key, constantBlock(0xAA))
	out := make([][]byte, updatedKeyCount)
	for i := range out {
		out[i] = encrypt(h, constantBlock(0xAA))
		h = encrypt(h, constantBlock(0x55))
	}
	return out
}

// eval walks input one nibble at a time, high nibble first. Each nibble picks a
// plaintext, which is encrypted under the running key to give the next one. The
// finalised form encrypts a zero block under the last, so the value handed out
// is never one the walk itself used as a key.
func (k *Key) eval(input []byte, final bool) []byte {
	y := k.updated[:]
	for _, b := range input {
		y = encrypt(y, k.plaintexts[b>>4][:])
		y = encrypt(y, k.plaintexts[b&0x0F][:])
	}
	if final {
		y = encrypt(y, make([]byte, BlockSize))
	}
	return y
}

// Encrypt enciphers data, a whole number of blocks, as LRICB (the primitive's
// counter mode). The counter is a big-endian integer of any length, and rises
// by one per block; each block is enciphered under the key the counter's
// current value evaluates to.
func (k *Key) Encrypt(counter, data []byte) ([]byte, error) {
	return k.lricb(counter, data, true)
}

// Decrypt reverses Encrypt under the same counter.
func (k *Key) Decrypt(counter, data []byte) ([]byte, error) {
	return k.lricb(counter, data, false)
}

func (k *Key) lricb(counter, data []byte, enc bool) ([]byte, error) {
	if len(counter) == 0 {
		return nil, errors.New("lrp: the counter is empty")
	}
	if len(data)%BlockSize != 0 {
		return nil, fmt.Errorf("lrp: data is %d bytes, want a multiple of %d", len(data), BlockSize)
	}
	ctr := append([]byte(nil), counter...)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += BlockSize {
		y := k.eval(ctr, true)
		if enc {
			copy(out[i:], encrypt(y, data[i:i+BlockSize]))
		} else {
			copy(out[i:], decrypt(y, data[i:i+BlockSize]))
		}
		increment(ctr)
	}
	return out, nil
}

// CMAC is CMAC_LRP: the AES-CMAC construction with each block's AES step
// replaced by the finalised evaluation, so the chaining value's nibbles walk
// the plaintexts. The result is the full 16 bytes.
func (k *Key) CMAC(msg []byte) []byte {
	k1 := doubled(k.eval(make([]byte, BlockSize), true))
	k2 := doubled(k1)

	head := 0
	if len(msg) > BlockSize {
		head = (len(msg) - 1) / BlockSize * BlockSize
	}

	chain := make([]byte, BlockSize)
	for i := 0; i < head; i += BlockSize {
		chain = k.eval(xorBlock(chain, msg[i:i+BlockSize]), true)
	}

	last := make([]byte, BlockSize)
	rest := msg[head:]
	copy(last, rest)
	if len(rest) == BlockSize {
		last = xorBlock(last, k1)
	} else {
		last[len(rest)] = 0x80
		last = xorBlock(last, k2)
	}
	return k.eval(xorBlock(chain, last), true)
}

// increment adds one to a big-endian counter, wrapping at its width.
func increment(ctr []byte) {
	for i := len(ctr) - 1; i >= 0; i-- {
		ctr[i]++
		if ctr[i] != 0 {
			return
		}
	}
}

// doubled multiplies a block by x in GF(2^128), as AES-CMAC derives its
// subkeys.
func doubled(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] << 1
		if i+1 < len(b) {
			out[i] |= b[i+1] >> 7
		}
	}
	if b[0]&0x80 != 0 {
		out[len(out)-1] ^= 0x87
	}
	return out
}

func xorBlock(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func constantBlock(v byte) []byte {
	b := make([]byte, BlockSize)
	for i := range b {
		b[i] = v
	}
	return b
}

// encrypt and decrypt are a single AES-128 block under a key that is always 16
// bytes here.
func encrypt(key, block []byte) []byte {
	c, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, BlockSize)
	c.Encrypt(out, block)
	return out
}

func decrypt(key, block []byte) []byte {
	c, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, BlockSize)
	c.Decrypt(out, block)
	return out
}
