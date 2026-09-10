// Package ev2 implements NXP's EV2 secure messaging: the authenticated channel
// a DESFire EV2, EV3 or NTAG 424 DNA opens once both sides prove they hold the
// same AES key.
//
// The pieces are an [Authenticator], which drives the three-pass exchange in two
// round trips, and the [Session] it leaves behind: two derived keys, the
// transaction identifier the card assigned, and a command counter. Both go into
// every MAC, so a captured command cannot be replayed into another session or
// twice into the same one.
//
// The transport is the caller's. Each step returns the APDU to send and consumes
// the card's answer, so the same code drives a PC/SC reader, a phone over the
// device protocol, or a test.
//
// The package touches no reader and imports nothing outside the standard
// library. AES-CMAC is implemented here, since the standard library has none,
// and is pinned to RFC 4493's vectors; every step of the exchange is pinned to
// the worked examples in NXP's AN12196.
//
// It is named for the generation of the protocol rather than for a card: the
// NTAG 424 DNA speaks the DESFire EV2 command set, and this is what they share.
package ev2
