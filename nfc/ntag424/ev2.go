package ntag424

import "github.com/dotside-studios/davi-nfc-agent/nfc/ev2"

// The authenticated channel this card opens is NXP's EV2 secure messaging,
// which it shares with the DESFire EV2 and EV3 it descends from. It lives in
// package ev2, and is named here so a caller driving an NTAG 424 reaches it
// without a second import.

type (
	// Session is an authenticated transaction. See [ev2.Session].
	Session = ev2.Session

	// Authenticator drives one authentication exchange. See [ev2.Authenticator].
	Authenticator = ev2.Authenticator

	// AuthMode selects which authentication a session starts with.
	AuthMode = ev2.AuthMode

	// CommMode is how much protection a command carries.
	CommMode = ev2.CommMode
)

const (
	AuthFirst    = ev2.AuthFirst
	AuthNonFirst = ev2.AuthNonFirst

	CommPlain = ev2.CommPlain
	CommMAC   = ev2.CommMAC
	CommFull  = ev2.CommFull

	// KeySize is the length of every key here: AES-128.
	KeySize = ev2.KeySize

	// MACSize is the length of a MAC on the wire, a CMAC truncated to half its
	// length. An SDMMAC is the same size.
	MACSize = ev2.MACSize
)

var (
	// ErrMACMismatch reports that a MAC did not match the data it covers,
	// whether on a tap or on a card's answer inside a session.
	ErrMACMismatch = ev2.ErrMACMismatch

	// ErrKeySize reports a key that is not AES-128.
	ErrKeySize = ev2.ErrKeySize

	// ErrAuthFailed reports that the card returned the wrong random number.
	ErrAuthFailed = ev2.ErrAuthFailed

	// ErrAuthState reports the authentication steps being taken out of order.
	ErrAuthState = ev2.ErrAuthState
)

// NewAuthenticator prepares an authentication exchange with the numbered key.
// See [ev2.NewAuthenticator].
func NewAuthenticator(mode AuthMode, keyNo byte, key, ti []byte) (*Authenticator, error) {
	return ev2.NewAuthenticator(mode, keyNo, key, ti)
}
