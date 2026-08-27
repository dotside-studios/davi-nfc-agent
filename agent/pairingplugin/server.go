package pairingplugin

import (
	"context"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
)

// Server runs a [pairing.Server]'s own listener as a component of the agent, so
// it starts and stops with the agent.
//
// The lifetime is all it adds. The pairing machinery belongs to whatever owns
// the credentials; see pairednfc.Manager.
type Server struct {
	server *pairing.Server
	port   int
}

var _ agent.Component = (*Server)(nil)

// NewServer wraps server as a component listening on port. It does not listen
// until the agent starts it.
func NewServer(server *pairing.Server, port int) *Server {
	return &Server{server: server, port: port}
}

// Name identifies the component.
func (p *Server) Name() string { return "pairing" }

// Start binds the pairing listener.
func (p *Server) Start(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.Listen(p.port)
}

// Stop closes it, which the agent does on the way down.
func (p *Server) Stop() error {
	if p == nil {
		return nil
	}
	p.server.Stop()
	return nil
}

// Port reports the port it listens on, 0 on a nil server.
func (p *Server) Port() int {
	if p == nil {
		return 0
	}
	return p.port
}

// PIN is the code a phone must present to pair, empty on a nil server.
//
// A build without pairing holds a nil *Server, so the nil receiver saves every
// caller a check.
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

// Server exposes the pairing server it runs, nil on a nil one.
func (p *Server) Server() *pairing.Server {
	if p == nil {
		return nil
	}
	return p.server
}
