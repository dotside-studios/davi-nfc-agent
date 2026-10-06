package ntag424

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

var originalityUID = []byte{0x04, 0x51, 0x2F, 0xCA, 0x21, 0x50, 0x80}

func signUID(t *testing.T, key *ecdsa.PrivateKey, uid []byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, key, uid)
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, SigSize)
	r.FillBytes(sig[:SigSize/2])
	s.FillBytes(sig[SigSize/2:])
	return sig
}

// AN12196 Rev. 2.0 Table 30: a genuine tag's UID and the signature it returned,
// which verify under NXP's key with the UID signed unhashed.
func TestVerifyOriginalityAN12196Table30(t *testing.T) {
	uid, _ := hex.DecodeString("04518DFAA96180")
	sig, _ := hex.DecodeString("D1940D17CFEDA4BFF80359AB975F9F6514313E8F90C1D3CAAF5941AD" +
		"744A1CDF9A83F883CAFE0FE95D1939B1B7E47113993324473B785D21")
	if !VerifyOriginality(uid, sig, nil) {
		t.Fatal("AN12196's genuine signature does not verify under NXP's key")
	}
	tampered := append([]byte(nil), uid...)
	tampered[6] ^= 0x01
	if VerifyOriginality(tampered, sig, nil) {
		t.Error("the signature verifies over another UID")
	}
}

func TestNXPOriginalityKeyIsOnTheCurve(t *testing.T) {
	pub := NXPOriginalityKey()
	if !elliptic.P224().IsOnCurve(pub.X, pub.Y) {
		t.Fatal("NXP originality key is not on secp224r1")
	}
}

func TestVerifyOriginalityRoundTripWithGeneratedKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig := signUID(t, key, originalityUID)
	if !VerifyOriginality(originalityUID, sig, &key.PublicKey) {
		t.Fatal("a signature over the UID does not verify")
	}

	bad := append([]byte(nil), sig...)
	bad[40] ^= 0x01
	if VerifyOriginality(originalityUID, bad, &key.PublicKey) {
		t.Error("a tampered signature verifies")
	}
	uid := append([]byte(nil), originalityUID...)
	uid[6] ^= 0x01
	if VerifyOriginality(uid, sig, &key.PublicKey) {
		t.Error("a tampered UID verifies")
	}
	if VerifyOriginality(originalityUID, sig, nil) {
		t.Error("a signature under another key verifies against NXP's key")
	}
	if VerifyOriginality(originalityUID, sig[:55], &key.PublicKey) || VerifyOriginality(originalityUID[:4], sig, &key.PublicKey) {
		t.Error("a malformed input verifies")
	}
	if VerifyOriginality(originalityUID, make([]byte, SigSize), &key.PublicKey) {
		t.Error("an all-zero signature verifies")
	}
}

func TestParseOriginalityKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseOriginalityKey(raw); err != nil {
		t.Errorf("valid key: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	if _, err := ParseOriginalityKey(raw); err == nil {
		t.Error("an off-curve key parsed")
	}
}
