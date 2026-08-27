// Package pairednfc holds the manager that decides which devices a build
// admits.
//
// It is a manager and a server in one component, the shape remotenfc
// established, except that it wraps endpoints rather than terminating one. It
// sits over whatever manager tree it is given, owns the credential store and
// the pairing machinery, and lends its credential check to the backends beneath
// it through [Manager.Admit].
//
// A build that wires no paired-device manager mounts its backends directly and
// admits every device that connects.
package pairednfc

import (
	"fmt"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/pairing"
	"github.com/dotside-studios/davi-nfc-agent/server"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
)

// Manager admits devices for the backends beneath it, and is the manager the
// agent holds. It builds its own registry and pairing server.
type Manager struct {
	child    nfc.Manager
	registry *pairing.Registry
	pairing  *pairing.Server
	policy   Policy

	// scans republishes the child's scans. See [Manager.Scans].
	scans event.Signal[nfc.ScannedTag]

	// revocations ends the session of a device whose credential was revoked.
	// Subscribed once, in New, so the check that admits a device cannot be
	// wired without the revocation that removes it again.
	revocations *event.Connection
}

var _ nfc.Manager = (*Manager)(nil)

// Options configures the manager.
type Options struct {
	// ConfigDir is where the registry persists. The manager loads or creates
	// its own there. A store it cannot read leaves the devices in memory.
	ConfigDir string

	// Registry overrides ConfigDir, for a build holding a store already and for
	// tests. Nil is the normal case.
	Registry *pairing.Registry

	// CA hands out the local certificate authority to a device that is pairing.
	// Nil for an agent serving an externally provisioned certificate, which has
	// no authority of its own to give. Also settable with
	// [Manager.UseCertificateAuthority].
	CA tlspkg.CertificateAuthority

	// AppName is the name the pairing pages present.
	AppName string

	// PublicKeyPin is what a device records to recognise this agent later, and
	// AgentPort is the port it is told to connect to afterwards. Both are read
	// per pairing; see [pairing.ServerOptions].
	PublicKeyPin func() string
	AgentPort    func() int

	// Policy is the credential policy. Also settable with [Manager.Require] and
	// [Manager.UseSecret], for a build assembling this before the agent.
	Policy Policy
}

// Policy is what the manager admits on. Both fields are read per request, so
// rotating the secret or changing the requirement needs nothing rebuilt.
type Policy struct {
	// Secret reports the shared API secret, a peer credential to a paired
	// token. Nil, or one returning empty, admits any device presenting no
	// paired credential.
	Secret func() string

	// RequirePaired drops the shared secret and the loopback bypass, admitting
	// only a device holding a credential issued at pairing. Nil is false.
	RequirePaired func() bool
}

func (p Policy) secret() string {
	if p.Secret == nil {
		return ""
	}
	return p.Secret()
}

func (p Policy) requirePaired() bool {
	return p.RequirePaired != nil && p.RequirePaired()
}

// New builds the manager over child, which is one backend or an aggregate.
//
// A registry that cannot be read is not an error: the devices are kept in
// memory and the failure is logged, so an agent whose config directory has gone
// missing still serves.
func New(child nfc.Manager, opts Options) (*Manager, error) {
	if child == nil {
		return nil, fmt.Errorf("pairednfc: a manager to admit devices for is required")
	}

	registry := opts.Registry
	if registry == nil {
		loaded, err := pairing.NewRegistry(opts.ConfigDir)
		if err != nil {
			pairedWarn.Printf("Could not read the paired devices: %v (keeping them in memory only)", err)
			loaded, _ = pairing.NewRegistry("")
		}
		registry = loaded
	}

	m := &Manager{
		child:    child,
		registry: registry,
		policy:   opts.Policy,
	}

	m.pairing = pairing.NewServer(pairing.ServerOptions{
		CA:           opts.CA,
		Registry:     registry,
		AppName:      opts.AppName,
		PublicKeyPin: opts.PublicKeyPin,
		AgentPort:    opts.AgentPort,
	})

	// The child publishes on its own and this passes it on. No goroutine, and
	// nothing buffered here to drop.
	nfc.OnScan(child, m.scans.Emit)

	// A credential is checked once, at the connection. Without this a device
	// revoked while connected keeps its session until it reconnects, which for
	// a heartbeating device is never.
	m.revocations = registry.OnRevoke(func(ids []string) {
		for _, id := range ids {
			if nfc.Disconnect(m.child, id, "device revoked") {
				pairedLog.Printf("Ended the session of revoked device %s", id)
			}
		}
	})

	return m, nil
}

// Require sets what decides whether only paired devices are admitted, for a
// build assembling this manager before the thing holding the preference.
func (m *Manager) Require(requirePaired func() bool) {
	m.policy.RequirePaired = requirePaired
}

// UseSecret sets the shared API secret, the peer credential to a paired token.
func (m *Manager) UseSecret(secret func() string) {
	m.policy.Secret = secret
}

// UseCertificateAuthority names the authority a pairing device is handed, for a
// build that settles its certificate after assembling this manager. Call it
// before the pairing listener starts.
func (m *Manager) UseCertificateAuthority(ca tlspkg.CertificateAuthority) {
	m.pairing.UseCertificateAuthority(ca)
}

// PairedDevices is the credential store, to list and revoke. Narrower than the
// registry: issuing and verifying stay inside this component.
//
// Named apart from [Manager.Devices], which lists what is connected. A device
// can be paired without being connected, and connected without being paired.
func (m *Manager) PairedDevices() pairing.Store { return m.registry }

// TokenVerifier recognises the credentials this manager issued, for an endpoint
// this manager does not wrap. The client endpoint takes it: a browser cannot
// pair, but a device connecting as a client carries the credential it paired
// with.
func (m *Manager) TokenVerifier() server.TokenVerifier { return m.registry }

// PairingServer is the machinery that issues credentials, for whatever runs its
// listener and shows its PIN.
func (m *Manager) PairingServer() *pairing.Server { return m.pairing }

// PairHandler serves pairing, to mount on the listener already serving the
// certificate that this agent's key pin covers.
func (m *Manager) PairHandler() http.Handler { return m.pairing.PairHandler() }

// Close releases the revocation subscription and closes the child.
func (m *Manager) Close() {
	if m.revocations != nil {
		m.revocations.Disconnect()
	}
	if closer, ok := m.child.(interface{ Close() }); ok {
		closer.Close()
	}
}
