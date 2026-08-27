// Package pairednfc holds the manager that decides which devices a build
// admits.
//
// It is a manager and a server in one component, the shape [remotenfc] already
// established, except that it wraps endpoints rather than terminating one. It
// sits as a parent over whatever manager tree it is given, owns the credential
// store and the pairing machinery behind it, and lends its credential check to
// the backends beneath it through [Manager.Admit].
//
// The point of assembling it this way is what happens when it is left out: a
// build that wires no paired-device manager mounts its backends directly, and
// every device that connects is admitted under an identity of the backend's own
// minting. Pairing is not a thing to switch off; it is a thing to bolt on.
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
// agent holds.
//
// It builds its own registry and pairing server: the machinery is the
// component, not something assembled beside it and handed in.
type Manager struct {
	child    nfc.Manager
	registry *pairing.Registry
	pairing  *pairing.Server
	policy   Policy

	// scans is the child's scans republished as this manager's own. See
	// [Manager.Scans].
	scans event.Signal[nfc.ScannedTag]

	// revocations ends the session of a device whose credential was revoked.
	// Subscribed once, here, so a build cannot wire the check that admits a
	// device without the revocation that removes it again.
	revocations *event.Connection
}

var _ nfc.Manager = (*Manager)(nil)

// Options configures the manager. Everything here is the build's decision; what
// it does with them is not.
type Options struct {
	// ConfigDir is where the registry persists. The manager loads or creates
	// its own there. A directory it cannot read leaves the devices in memory
	// and reports the error, rather than failing to build.
	ConfigDir string

	// Registry overrides that, for a build holding one already and for tests.
	// Nil is the normal case.
	Registry *pairing.Registry

	// CA hands out the local certificate authority to a device that is pairing.
	// Nil is normal: an agent serving an externally provisioned certificate has
	// no authority of its own to give.
	CA tlspkg.CertificateAuthority

	// AppName is the name the pairing pages present.
	AppName string

	// PublicKeyPin is what a device records to recognise this agent later, and
	// AgentPort is the port it is told to connect to afterwards. Both are read
	// per pairing rather than captured; see [pairing.ServerOptions].
	PublicKeyPin func() string
	AgentPort    func() int

	// Policy is the credential policy. It can also be set afterwards with
	// [Manager.Require] and [Manager.UseSecret], which is what a build assembling
	// this before the agent needs.
	Policy Policy
}

// Policy is what the manager admits on, read per request so that rotating a
// secret or changing the paired-device requirement needs nothing rebuilt.
type Policy struct {
	// Secret reports the shared API secret, a peer credential to a paired
	// token. Nil, or one returning empty, admits every device that presents no
	// paired credential — which is what a build with no secret and no pairing
	// has always done.
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

// New builds the manager over child, which is one backend or an aggregate of
// them.
//
// It returns an error only for what makes the manager unusable. A registry that
// cannot be read from disk is not that: the devices are kept in memory and the
// error is reported, so an agent whose config directory has gone missing still
// serves rather than refusing to start.
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

	// The child publishes on its own and this passes it on; no goroutine, and
	// nothing buffered here to drop.
	nfc.OnScan(child, m.scans.Emit)

	// Subscribed here rather than by whoever mounts an endpoint. A credential
	// is checked once, at the connection, so without this a device revoked
	// while connected keeps its session until it reconnects, which for a
	// heartbeating device is never.
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
// build that assembles this manager before the thing holding the preference.
func (m *Manager) Require(requirePaired func() bool) {
	m.policy.RequirePaired = requirePaired
}

// UseSecret sets the shared API secret, the peer credential to a paired token,
// for the same reason as [Manager.Require].
func (m *Manager) UseSecret(secret func() string) {
	m.policy.Secret = secret
}

// PairedDevices is the credential store, to list and revoke.
//
// Deliberately narrower than the registry: minting a credential and checking
// one stay inside this component, with the pairing server that issues them. It
// is named apart from [Manager.Devices], the manager's listing of what is
// connected — a device can be paired without being here, and connected without
// being paired.
func (m *Manager) PairedDevices() pairing.Store { return m.registry }

// TokenVerifier recognises the credentials this manager issued, for an endpoint
// admitting a device token that this manager does not wrap. The client
// endpoint takes it: a browser cannot pair, but a device connecting as a client
// carries the credential it paired with.
func (m *Manager) TokenVerifier() server.TokenVerifier { return m.registry }

// UseCertificateAuthority names the authority a pairing device is handed, for
// a build that settles its certificate after assembling this manager. That is
// the normal order: the manager is built to be handed to the agent, and the
// agent is what resolves the certificate.
func (m *Manager) UseCertificateAuthority(ca tlspkg.CertificateAuthority) {
	m.pairing.UseCertificateAuthority(ca)
}

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
