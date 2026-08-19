package agent

import (
	"context"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
)

// PairingConfig is what the pairing server needs, and nothing the rest of the
// agent has to carry. Before this, the agent held the built server and its port
// as two more fields of its own, started it in Setup and never stopped it --
// only the command's signal handler did.
type PairingConfig struct {
	// Port is the pairing server's own listener. Required; a zero port means
	// the caller did not want pairing, so do not build the component at all.
	Port int

	// CA hands out the local certificate authority, to a request carrying the
	// PIN. Nil is normal: an agent using an externally provisioned certificate
	// has no CA to give out, and pairing works regardless.
	CA *tlspkg.Manager

	// Devices is the registry a paired device's credential is issued into.
	Devices *DeviceRegistry

	// PublicKeyPin is what a device records to recognise this agent later.
	PublicKeyPin string

	// AppName is the name the pairing pages present.
	AppName string

	// AgentPort is the port a paired device is told to connect to afterwards.
	AgentPort int
}

// PairingServer runs the pairing and CA-distribution listener as a component of
// the agent, so it starts and stops with the agent rather than being started in
// Setup and stopped by whoever remembers to.
type PairingServer struct {
	cfg    PairingConfig
	server *tlspkg.BootstrapServer
}

var _ Component = (*PairingServer)(nil)

// NewPairingServer builds the component. It does not listen until the agent
// starts it.
func NewPairingServer(cfg PairingConfig) *PairingServer {
	srv := tlspkg.NewBootstrapServer(cfg.CA, cfg.Port)

	srv.SetAppName(cfg.AppName)
	if cfg.Devices != nil {
		srv.SetPairingIssuer(NewPairingIssuer(cfg.Devices, cfg.PublicKeyPin), cfg.AgentPort)
	}

	return &PairingServer{cfg: cfg, server: srv}
}

// Name identifies the component.
func (p *PairingServer) Name() string { return "pairing" }

// Start binds the pairing listener.
func (p *PairingServer) Start(ctx context.Context) error {
	return p.server.Start()
}

// Stop closes it. The agent calls this on the way down, which is the part that
// used to be missing.
func (p *PairingServer) Stop() error {
	p.server.Stop()
	return nil
}

// Port reports the port it listens on.
func (p *PairingServer) Port() int { return p.cfg.Port }

// Server exposes the underlying bootstrap server, for the PIN and the URLs the
// tray and the console display.
func (p *PairingServer) Server() *tlspkg.BootstrapServer { return p.server }
