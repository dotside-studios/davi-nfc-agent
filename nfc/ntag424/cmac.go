package ntag424

import (
	"crypto/aes"
	"crypto/cipher"
)

// AES-CMAC (NIST SP 800-38B, RFC 4493). The standard library has none and the
// agent core carries no third-party dependencies, so it is implemented here and
// pinned to the RFC's vectors.

// blockSize is the AES block size, and the size of a CMAC.
const blockSize = aes.BlockSize

// cmacSubkeyConstant is the reduction polynomial's low byte for a 128-bit
// block, applied when a left shift overflows.
const cmacSubkeyConstant = 0x87

// cmac returns the AES-CMAC of msg under key. The message may be empty, which a
// tag mirroring nothing but PICCData relies on.
func cmac(block cipher.Block, msg []byte) []byte {
	k1, k2 := cmacSubkeys(block)

	// The last block is XORed with k1 when it is whole, or padded and XORed
	// with k2 when it is short or the message is empty.
	var last [blockSize]byte
	complete := len(msg) > 0 && len(msg)%blockSize == 0

	var head []byte
	if complete {
		head = msg[:len(msg)-blockSize]
		xor(last[:], msg[len(msg)-blockSize:], k1)
	} else {
		n := len(msg) - len(msg)%blockSize
		head = msg[:n]
		var padded [blockSize]byte
		copy(padded[:], msg[n:])
		padded[len(msg)-n] = 0x80
		xor(last[:], padded[:], k2)
	}

	// CBC-MAC over the whole message: the chaining value starts at zero, and
	// the tag is the encryption of the final block.
	var chain [blockSize]byte
	for i := 0; i < len(head); i += blockSize {
		xor(chain[:], chain[:], head[i:i+blockSize])
		block.Encrypt(chain[:], chain[:])
	}
	xor(chain[:], chain[:], last[:])
	block.Encrypt(chain[:], chain[:])

	out := make([]byte, blockSize)
	copy(out, chain[:])
	return out
}

// cmacSubkeys derives K1 and K2 from the cipher, per RFC 4493 section 2.3.
func cmacSubkeys(block cipher.Block) (k1, k2 []byte) {
	l := make([]byte, blockSize)
	block.Encrypt(l, l) // L = E(K, 0^128)

	k1 = shiftLeft(l)
	k2 = shiftLeft(k1)
	return k1, k2
}

// shiftLeft returns b shifted left by one bit, reduced by the block
// polynomial when the shift overflows.
func shiftLeft(b []byte) []byte {
	out := make([]byte, len(b))
	overflow := b[0]&0x80 != 0
	for i := 0; i < len(b); i++ {
		out[i] = b[i] << 1
		if i+1 < len(b) {
			out[i] |= b[i+1] >> 7
		}
	}
	if overflow {
		out[len(out)-1] ^= cmacSubkeyConstant
	}
	return out
}

// xor writes a^b into dst, which may alias either input.
func xor(dst, a, b []byte) {
	for i := range dst {
		dst[i] = a[i] ^ b[i]
	}
}
