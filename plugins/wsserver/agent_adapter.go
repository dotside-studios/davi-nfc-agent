package wsserver

import (
	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// ForAgent adapts the agent this repository ships to [Agent], for a build that
// serves the standard one.
//
// A build with an agent of its own implements the interface instead; this is
// the convenience, not the contract.
func ForAgent(a *agent.Agent) Agent { return &servingAgent{agent: a} }

// servingAgent is that adapter. Every reach the servers make into the agent is
// a method here, so what a listener reads is a list rather than a search, and
// each is read again on every restart rather than taken once and kept: a
// listener that came back after a rotated secret has to come back with the new
// one.
type servingAgent struct {
	agent *agent.Agent
}

var _ Agent = (*servingAgent)(nil)

func (s *servingAgent) Reader() *nfc.NFCReader { return s.agent.Reader }

func (s *servingAgent) RemoteDevices() *remotenfc.Manager { return s.agent.RemoteDevices() }

func (s *servingAgent) APISecret() string { return s.agent.APISecret }

// RotateAPISecret issues a fresh one and restarts the listeners on to it, which
// takes this plugin down and back up with the new secret in hand.
func (s *servingAgent) RotateAPISecret() (string, error) { return s.agent.RotateAPISecret() }

func (s *servingAgent) PublicKeyPin() string { return s.agent.PublicKeyPin }

func (s *servingAgent) TokenVerifier() server.TokenVerifier { return s.agent.TokenVerifier() }

func (s *servingAgent) OriginPolicy() server.OriginPolicy { return s.agent.OriginPolicy() }

func (s *servingAgent) AllowedOrigins() []string { return s.agent.AllowedOrigins }

func (s *servingAgent) AllowedCardTypes() map[string]bool { return s.agent.AllowedCardTypes }

func (s *servingAgent) RequirePairedDevice() bool { return s.agent.RequiresPairedDevice() }

func (s *servingAgent) Port() int { return s.agent.ConfiguredPort() }

func (s *servingAgent) Certificates() (certFile, keyFile string) {
	return s.agent.CertFile, s.agent.KeyFile
}

// ClientsChanged publishes the agent's state, which is how everything that
// renders a client list hears that one moved.
func (s *servingAgent) ClientsChanged() { s.agent.PublishState() }
