package main

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// servingAgent adapts the agent to wsserver.Agent, the way webuiHost adapts it
// to the console. Every reach the servers make into the agent is a method here,
// so what a listener reads is a list rather than a search.
//
// Each is read again on every restart, never taken once and kept: a listener
// that came back after a rotated secret has to come back with the new one.
type servingAgent struct {
	agent *Agent
}

var _ wsserver.Agent = (*servingAgent)(nil)

func (s *servingAgent) Reader() *nfc.NFCReader { return s.agent.Reader }

func (s *servingAgent) RemoteDevices() *remotenfc.Manager { return findDeviceDriver(s.agent.Manager) }

func (s *servingAgent) APISecret() string { return s.agent.APISecret }

// RotateAPISecret issues a fresh one and restarts the listeners on to it, which
// takes this plugin down and back up with the new secret in hand.
func (s *servingAgent) RotateAPISecret() (string, error) { return s.agent.RotateAPISecret() }

func (s *servingAgent) PublicKeyPin() string { return s.agent.PublicKeyPin }

func (s *servingAgent) TokenVerifier() server.TokenVerifier { return s.agent.tokenVerifier() }

func (s *servingAgent) OriginPolicy() server.OriginPolicy { return s.agent.originPolicy() }

func (s *servingAgent) AllowedOrigins() []string { return s.agent.AllowedOrigins }

func (s *servingAgent) AllowedCardTypes() map[string]bool { return s.agent.AllowedCardTypes }

func (s *servingAgent) RequirePairedDevice() bool { return s.agent.RequiresPairedDevice() }

func (s *servingAgent) Port() int { return s.agent.ConfiguredPort() }

func (s *servingAgent) Certificates() (certFile, keyFile string) {
	return s.agent.CertFile, s.agent.KeyFile
}

// ClientsChanged redraws the console when a client connects or disconnects.
func (s *servingAgent) ClientsChanged() {
	if changed := s.agent.clientsChanged(); changed != nil {
		changed()
	}
}
