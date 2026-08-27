package pairing

import "github.com/dotside-studios/davi-nfc-agent/event"

// Store is a credential registry to read and revoke: the only half anything
// outside the pairing machinery needs.
//
// Minting a credential and checking one are absent on purpose. They belong to
// whatever owns the registry, which is the component serving pairing.
//
// An interface rather than *Registry because a store for credentials this agent
// must present, for a reader it dials out to rather than one that dials in,
// answers these same six questions over quite different state. [Registry]
// cannot serve that: it keeps only a hash.
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

// Registry is the inbound store: it verifies credentials devices present, so it
// keeps only their hashes.
var _ Store = (*Registry)(nil)
