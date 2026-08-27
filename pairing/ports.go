package pairing

import "github.com/dotside-studios/davi-nfc-agent/event"

// Store is a credential registry to read and revoke: the half that does not
// depend on which direction the credential points, and the only half anything
// outside the pairing machinery needs.
//
// A tray listing paired devices and a console revoking one want exactly this
// and nothing more. Minting a credential and checking one are deliberately
// absent: they belong to whatever owns the registry, which is the component
// serving pairing, and handing them out is how a credential store turns into a
// thing every caller can quietly grow a dependency on.
//
// It is an interface rather than *Registry because a store for credentials this
// agent must *present* — a reader it dials out to, rather than one that dials
// in — answers these same six questions while storing something quite
// different. [Registry] cannot serve that: it keeps only a hash, on purpose.
type Store interface {
	// List returns the paired devices, most recently paired first.
	List() []Device

	// Count reports how many devices are paired.
	Count() int

	// Revoke removes one device, leaving every other device working.
	Revoke(id string) error

	// RevokeAll clears the store, for a machine changing hands.
	RevokeAll() error

	// OnChange registers fn to run after each pairing and revocation.
	OnChange(fn func()) *event.Connection

	// OnRevoke registers fn with the IDs whose credentials just stopped being
	// valid, so whatever holds live sessions can end the matching ones.
	OnRevoke(fn func(ids []string)) *event.Connection
}

// Registry is the inbound store: it verifies credentials devices present, and
// so keeps only their hashes.
var _ Store = (*Registry)(nil)
