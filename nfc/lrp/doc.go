// Package lrp implements the Leakage Resilient Primitive NXP specifies in
// AN12304: the cipher suite an NTAG 424 DNA or DESFire EV3 can be switched to
// in place of AES.
//
// LRP never uses a long-lived key as an AES key directly. A key is expanded into
// sixteen plaintexts and a few updated keys, and each block it protects is
// processed by walking a counter or message one nibble at a time, encrypting the
// plaintext that nibble selects under the running key. An attacker measuring one
// step learns about one nibble's worth of key material, not the whole key.
//
// The package has the pieces the protocol is built from: the plaintexts and
// updated keys, the evaluation they feed, LRICB encryption and CMAC_LRP. It
// touches no reader and imports nothing outside the standard library.
//
// The tests pin the plaintext generation, the first three updated keys, LRICB
// and CMAC_LRP to the worked examples of NXP's LRP specification.
package lrp
