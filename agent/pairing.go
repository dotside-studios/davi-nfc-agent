package agent

import (
	"context"

	"github.com/dotside-studios/davi-nfc-agent/pairing"
)

// PairingServer runs a [pairing.Server]'s own listener as a component of the
// agent, so it starts and stops with the agent.
//
// This is only the lifetime. The pairing machinery lives with whatever owns the
// credentials, which is the paired-device manager.
type PairingServer struct {
	server *pairing.Server
	port   int
}

var _ Component = (*PairingServer)(nil)

// NewPairingServer wraps server as a component listening on port. It does not
// listen until the agent starts it.
func NewPairingServer(server *pairing.Server, port int) *PairingServer {
	return &PairingServer{server: server, port: port}
}

// Name identifies the component.
func (p *PairingServer) Name() string { return "pairing" }

// Start binds the pairing listener.
func (p *PairingServer) Start(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.Listen(p.port)
}

// Stop closes it, which the agent does on the way down.
func (p *PairingServer) Stop() error {
	if p == nil {
		return nil
	}
	p.server.Stop()
	return nil
}

// Port reports the port it listens on, 0 on a nil server.
func (p *PairingServer) Port() int {
	if p == nil {
		return 0
	}
	return p.port
}

// PIN is the code a phone must present to pair, empty on a nil server.
//
// A build without pairing holds a nil *PairingServer, so the nil receiver
// saves every caller a check.
func (p *PairingServer) PIN() string {
	if p == nil {
		return ""
	}
	return p.server.PIN()
}

// RotatePIN issues a fresh PIN and returns it, invalidating the pairing URLs
// carrying the old one. Empty on a nil server.
func (p *PairingServer) RotatePIN() string {
	if p == nil {
		return ""
	}
	return p.server.RotatePIN()
}

// Server exposes the pairing server it runs, nil on a nil one.
func (p *PairingServer) Server() *pairing.Server {
	if p == nil {
		return nil
	}
	return p.server
}
