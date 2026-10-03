package ntag424

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
