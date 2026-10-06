package ntag424

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"fmt"
	"math/big"
)

// OriginalityKeyHex is NXP's public key for the NTAG 424 DNA originality
// signature, an uncompressed secp224r1 point (0x04, X, Y), as AN12196 section
// 7.2 publishes it. Pass a key to VerifyOriginality to override it.
const OriginalityKeyHex = "048A9B380AF2EE1B98DC417FECC263F8449C7625CECE82D9B916C992DA" +
	"209D68422B81EC20B65A66B5102A61596AF3379200599316A00A1410"

const originalityScalarSize = SigSize / 2

// NXPOriginalityKey returns NXP's NTAG 424 DNA originality public key.
func NXPOriginalityKey() *ecdsa.PublicKey {
	raw, err := hex.DecodeString(OriginalityKeyHex)
	if err != nil {
		panic(err)
	}
	pub, err := ParseOriginalityKey(raw)
	if err != nil {
		panic(err)
	}
	return pub
}

// ParseOriginalityKey reads an uncompressed secp224r1 public key (0x04, X, Y)
// and checks that the point lies on the curve.
func ParseOriginalityKey(raw []byte) (*ecdsa.PublicKey, error) {
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P224(), raw)
	if err != nil {
		return nil, fmt.Errorf("ntag424: originality key: %w", err)
	}
	return pub, nil
}

// VerifyOriginality reports whether sig, the 56-byte r||s signature ReadSig
// returns, is a valid ECDSA signature over uid under pub. The UID is the card's
// real 7-byte UID and is signed unhashed: its bytes are used directly as the
// ECDSA message representative. A nil pub means NXP's key.
func VerifyOriginality(uid, sig []byte, pub *ecdsa.PublicKey) bool {
	if len(uid) != 7 || len(sig) != SigSize {
		return false
	}
	if pub == nil {
		pub = NXPOriginalityKey()
	}
	r := new(big.Int).SetBytes(sig[:originalityScalarSize])
	s := new(big.Int).SetBytes(sig[originalityScalarSize:])
	return ecdsa.Verify(pub, uid, r, s)
}
