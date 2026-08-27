package pairingplugin

import (
	"context"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
)

// ServerConfig is what the pairing server needs, and nothing the rest of the
// agent has to carry.
type ServerConfig struct {
	// Port is the pairing server's own listener. Required; a zero port means
	// the caller did not want pairing, so do not build the component at all.
	Port int

	// CA hands out the local certificate authority, to a request carrying the
	// PIN. Nil is normal: an agent using an externally provisioned certificate
	// has no CA to give out, and pairing works regardless.
	//
	// Two methods rather than the whole TLS manager, since that is all the
	// pairing server reads: certificate material it does not serve is not its
	// to hold.
	CA tlspkg.CertificateAuthority

	// Devices is the registry a paired device's credential is issued into.
	Devices *agent.DeviceRegistry

	// PublicKeyPin is what a device records to recognise this agent later.
	PublicKeyPin string

	// AppName is the name the pairing pages present.
	AppName string

	// AgentPort is the port a paired device is told to connect to afterwards.
	AgentPort int
}

// ServerFor builds a pairing server for a, listening on port and handing out
// ca. Everything else it needs is what the agent was already configured with:
// its device registry, its key pin, its name and its port.
//
// ca may be nil: an agent serving an externally provisioned certificate has no
// authority to give out, and pairing works regardless.
//
// The agent does not hold the result. Whoever builds one registers it, as a
// component or as an endpoint of the server plugin, and hands it to whatever
// displays the PIN.
func ServerFor(a *agent.Agent, port int, ca tlspkg.CertificateAuthority) *Server {
	return NewServer(ServerConfig{
		Port:         port,
		CA:           ca,
		Devices:      a.Devices(),
		PublicKeyPin: a.PublicKeyPin(),
		AppName:      a.Info().DisplayName,
		AgentPort:    a.DevicePort(),
	})
}

// Server runs the pairing and CA-distribution listener as a component of
// the agent, so it starts and stops with the agent rather than being started in
// Setup and stopped by whoever remembers to.
type Server struct {
	cfg    ServerConfig
	server *tlspkg.BootstrapServer
}

var _ agent.Component = (*Server)(nil)

// NewServer builds the component. It does not listen until the agent
// starts it.
func NewServer(cfg ServerConfig) *Server {
	srv := tlspkg.NewBootstrapServer(cfg.CA, cfg.Port)

	srv.SetAppName(cfg.AppName)
	if cfg.Devices != nil {
		srv.SetPairingIssuer(NewIssuer(cfg.Devices, cfg.PublicKeyPin), cfg.AgentPort)
	}

	return &Server{cfg: cfg, server: srv}
}

// Name identifies the component.
func (p *Server) Name() string { return "pairing" }

// Start binds the pairing listener.
func (p *Server) Start(ctx context.Context) error {
	return p.server.Start()
}

// Stop closes it, which the agent does on the way down.
func (p *Server) Stop() error {
	p.server.Stop()
	return nil
}

// Port reports the port it listens on, 0 on a nil server.
func (p *Server) Port() int {
	if p == nil {
		return 0
	}
	return p.cfg.Port
}

// PIN is the code a phone must present to pair, empty on a nil server.
//
// A build without pairing holds a nil *Server, so the nil receiver
// saves every caller a check.
func (p *Server) PIN() string {
	if p == nil {
		return ""
	}
	return p.server.PIN()
}

// RotatePIN issues a fresh PIN and returns it, invalidating the pairing URLs
// carrying the old one. Empty on a nil server.
func (p *Server) RotatePIN() string {
	if p == nil {
		return ""
	}
	return p.server.RotatePIN()
}

// Server exposes the underlying bootstrap server, nil on a nil one.
func (p *Server) Server() *tlspkg.BootstrapServer {
	if p == nil {
		return nil
	}
	return p.server
}

// issuer adapts the device registry to the bootstrap server's issuer
// interface, which is deliberately narrow: the bootstrap server owns the PIN
// and the proof-of-presence, and knows nothing about how devices are stored.
type issuer struct {
	registry *agent.DeviceRegistry
	pin      string
}

func (i issuer) Pair(name, platform string) (string, string, error) {
	device, token, err := i.registry.Pair(name, platform)
	if err != nil {
		return "", "", err
	}
	return device.ID, token, nil
}

func (i issuer) PublicKeyPin() string { return i.pin }

// NewIssuer returns an issuer backed by registry, reporting pin as the agent's
// identity to newly paired devices.
func NewIssuer(registry *agent.DeviceRegistry, pin string) tlspkg.PairingIssuer {
	return issuer{registry: registry, pin: pin}
}
