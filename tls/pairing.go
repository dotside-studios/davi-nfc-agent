package tls

import (
	"encoding/json"
	"net/http"
)

// PairingIssuer mints a credential for a device that has proven it can see the
// kiosk, by presenting the PIN.
//
// Implemented outside this package: the bootstrap server owns the PIN and the
// proof-of-presence, not the device registry.
type PairingIssuer interface {
	// Pair registers a device and returns its ID and token. The token is
	// returned once and not recoverable afterwards.
	Pair(name, platform string) (id string, token string, err error)

	// PublicKeyPin identifies the agent, so a device can recognize it on later
	// connections without trusting a certificate authority.
	PublicKeyPin() string
}

// PairingRequest is the body a device posts to /pair.
type PairingRequest struct {
	DeviceName string `json:"deviceName"`
	Platform   string `json:"platform"`
}

// PairingResponse is everything a device needs to connect from then on: who it
// is, how to authenticate, and how to recognize this agent again.
type PairingResponse struct {
	DeviceID     string `json:"deviceID"`
	DeviceToken  string `json:"deviceToken"`
	PublicKeyPin string `json:"publicKeyPin,omitempty"`
	AgentPort    int    `json:"agentPort"`
}

// SetPairingIssuer enables the /pair endpoint. Without one the endpoint reports
// that pairing is unavailable rather than 404, so a device can tell an agent
// that cannot pair from a wrong address.
func (s *BootstrapServer) SetPairingIssuer(issuer PairingIssuer, agentPort int) {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	s.pairIssuer = issuer
	s.agentPort = agentPort
}

// handlePair issues a per-device credential to a caller that knows the PIN.
//
// The PIN is proof the operator can see the kiosk, and it is rate-limited and
// locks out after repeated failures, the same gate that protects the CA
// download, reused because it answers the same question.
func (s *BootstrapServer) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Pairing requires POST.", http.StatusMethodNotAllowed)
		return
	}

	s.pairMu.RLock()
	issuer, agentPort := s.pairIssuer, s.agentPort
	s.pairMu.RUnlock()

	if issuer == nil {
		http.Error(w, "Pairing is not enabled on this agent.", http.StatusNotImplemented)
		return
	}

	if !s.requirePIN(w, r) {
		return
	}

	var req PairingRequest
	if r.Body != nil {
		// A body is optional: a device that sends nothing still pairs, it just
		// shows up unnamed in the operator's device list.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	id, token, err := issuer.Pair(req.DeviceName, req.Platform)
	if err != nil {
		s.logger.Printf("Pairing failed for %s: %v", r.RemoteAddr, err)
		http.Error(w, "Pairing failed.", http.StatusInternalServerError)
		return
	}

	s.logger.Printf("Paired device %s (%s) from %s", id, req.DeviceName, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(PairingResponse{
		DeviceID:     id,
		DeviceToken:  token,
		PublicKeyPin: issuer.PublicKeyPin(),
		AgentPort:    agentPort,
	})
}
