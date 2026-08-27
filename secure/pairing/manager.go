// Package admission decides which devices a build admits.
//
// It owns the credential store and the pairing server behind it, and lends its
// check to a backend's endpoint through [Gate.Admit]. The backend itself carries
// no authentication: whatever this admits is named on the request, and the
// backend registers the device under that name. See server/deviceid.
//
// A build that wires no gate mounts its backends directly and admits every
// device that connects.
package pairing

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/event"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Sessions is the part of a device backend this package needs: a way to end the
// session held under an identity.
//
// Declared here so pairing names no NFC package. nfc.DeviceDisconnector is the
// same method set, and Go satisfies both.
type Sessions interface {
	// DisconnectDevice ends the session held by deviceID, reporting whether
	// there was one. reason is what the device is told.
	DisconnectDevice(deviceID, reason string) bool
}

// Gate admits devices for the endpoints it wraps.
//
// It holds no manager. What it needs of one is [Sessions]: a credential is
// checked when a device connects, so revoking one has to reach the session it
// already holds.
type Gate struct {
	sessions Sessions
	registry *Registry
	pairing  *Server
	policy   Policy

	// revocations ends the session of a device whose credential was revoked.
	// Subscribed once, in New, so the check that admits a device cannot be
	// wired without the revocation that removes it again.
	revocations *event.Connection
}

// Options configures the gate.
type Options struct {
	// ConfigDir is where the registry persists. The gate loads or creates its
	// own there. A store it cannot read leaves the devices in memory.
	ConfigDir string

	// Registry overrides ConfigDir, for a build holding a store already and for
	// tests. Nil is the normal case.
	Registry *Registry

	// CA hands out the local certificate authority to a device that is pairing.
	// Nil for an agent serving an externally provisioned certificate, which has
	// no authority of its own to give.
	CA tlspkg.CertificateAuthority

	// AppName is the name the pairing pages present.
	AppName string

	// PublicKeyPin is what a device records to recognise this agent later, and
	// AgentPort is the port it is told to connect to afterwards. Both are read
	// per pairing; see [ServerOptions].
	PublicKeyPin func() string
	AgentPort    func() int

	// Policy is the credential policy. Also settable with [Gate.Require],
	// [Gate.UseSecret] and [Gate.AllowLoopback], for a build assembling this
	// before the agent.
	Policy Policy
}

// Policy is what the gate admits on. Every field is read per request, so
// rotating the secret or changing a requirement needs nothing rebuilt.
type Policy struct {
	// Secret reports the shared API secret, a peer credential to a paired
	// token. Nil, or one returning empty, admits any device presenting no
	// paired credential.
	Secret func() string

	// RequirePaired drops the shared secret and the loopback bypass, admitting
	// only a device holding a credential issued at pairing. Nil is false.
	RequirePaired func() bool

	// AllowLoopback admits a request from this host with no credential at all.
	// Nil is false: loopback names the host, so it also admits other accounts
	// on it, local proxies, and port forwards into it. See
	// [server.AuthOptions.AllowLoopback].
	AllowLoopback func() bool
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

func (p Policy) allowLoopback() bool {
	return p.AllowLoopback != nil && p.AllowLoopback()
}

// New builds the gate. sessions is what a revocation reaches through, which for
// a build with a manager tree is that tree; nil for one whose backends hold no
// sessions to end.
//
// A registry that cannot be read is not an error: the devices are kept in
// memory and the failure is logged, so an agent whose config directory has gone
// missing still serves.
func New(sessions Sessions, opts Options) *Gate {
	registry := opts.Registry
	if registry == nil {
		loaded, err := NewRegistry(opts.ConfigDir)
		if err != nil {
			admitWarn.Printf("Could not read the paired devices: %v (keeping them in memory only)", err)
			loaded, _ = NewRegistry("")
		}
		registry = loaded
	}

	g := &Gate{
		sessions: sessions,
		registry: registry,
		policy:   opts.Policy,
	}

	g.pairing = NewServer(ServerOptions{
		CA:           opts.CA,
		Registry:     registry,
		AppName:      opts.AppName,
		PublicKeyPin: opts.PublicKeyPin,
		AgentPort:    opts.AgentPort,
	})

	// A credential is checked once, at the connection. Without this a device
	// revoked while connected keeps its session until it reconnects, which for
	// a heartbeating device is never.
	g.revocations = registry.OnRevoke(func(ids []string) {
		for _, id := range ids {
			if g.sessions != nil && g.sessions.DisconnectDevice(id, "device revoked") {
				admitLog.Printf("Ended the session of revoked device %s", id)
			}
		}
	})

	return g
}

// Require sets what decides whether only paired devices are admitted, for a
// build assembling this gate before the thing holding the preference.
func (g *Gate) Require(requirePaired func() bool) {
	g.policy.RequirePaired = requirePaired

	// Requiring pairing with nothing paired refuses every device. Said here
	// because this is what holds both the requirement and the store.
	if g.policy.requirePaired() && g.registry.Count() == 0 {
		admitWarn.Printf("Paired devices are required and none are paired: every device will be refused until one pairs")
	}
}

// UseSecret sets the shared API secret, the peer credential to a paired token.
func (g *Gate) UseSecret(secret func() string) { g.policy.Secret = secret }

// AllowLoopback sets whether a request from this host is admitted with no
// credential. See [Policy.AllowLoopback].
func (g *Gate) AllowLoopback(allow func() bool) { g.policy.AllowLoopback = allow }

// UsePort sets the port a paired device is told to connect to afterwards, for a
// build that learns it after assembling this gate. Read per pairing either way.
func (g *Gate) UsePort(agentPort func() int) { g.pairing.UsePort(agentPort) }

// PairedDevices is the credential store, to list and revoke. Narrower than the
// registry: issuing and verifying stay in here.
func (g *Gate) PairedDevices() Store { return g.registry }

// TokenVerifier recognises the credentials this gate issued, for an endpoint it
// does not wrap. The client endpoint takes it: a browser cannot pair, but a
// device connecting as a client carries the credential it paired with.
func (g *Gate) TokenVerifier() server.TokenVerifier { return g.registry }

// PairingServer is the machinery that issues credentials, for whatever runs its
// listener and shows its PIN.
func (g *Gate) PairingServer() *Server { return g.pairing }

// PairHandler serves pairing, to mount on the listener already serving the
// certificate that this agent's key pin covers.
func (g *Gate) PairHandler() http.Handler { return g.pairing.PairHandler() }

// Close releases the revocation subscription. It closes no backend: the gate
// holds none, only a way to end a session on one.
func (g *Gate) Close() {
	if g.revocations != nil {
		g.revocations.Disconnect()
	}
}
