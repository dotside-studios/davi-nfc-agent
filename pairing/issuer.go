package pairing

import (
	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
)

// pairingIssuer adapts the registry to the bootstrap server's issuer
// interface, which is deliberately narrow: the bootstrap server owns the PIN
// and the proof-of-presence, and knows nothing about how devices are stored.
type pairingIssuer struct {
	registry *Registry
	pin      func() string
}

func (p pairingIssuer) Pair(name, platform string) (string, string, error) {
	device, token, err := p.registry.Pair(name, platform)
	if err != nil {
		return "", "", err
	}
	return device.ID, token, nil
}

// PublicKeyPin reports the agent's identity as it stands now. Read per pairing
// rather than captured: the pin follows the certificate, which can be reissued
// while the endpoint stays up.
func (p pairingIssuer) PublicKeyPin() string {
	if p.pin == nil {
		return ""
	}
	return p.pin()
}

// NewPairingIssuer returns an issuer backed by this registry, reporting pin as
// the agent's identity to newly paired devices.
func NewPairingIssuer(registry *Registry, pin func() string) tlspkg.PairingIssuer {
	return pairingIssuer{registry: registry, pin: pin}
}
