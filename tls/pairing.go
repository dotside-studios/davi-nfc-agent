package tls

import (
	"encoding/json"
	"net"
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
// agentPort is a function because it is read per pairing: the port the agent
// serves on can be changed from the control center while this endpoint stays
// up, and a device handed the stale one cannot connect.
func (s *BootstrapServer) SetPairingIssuer(issuer PairingIssuer, agentPort func() int) {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	s.pairIssuer = issuer
	s.agentPort = agentPort
}

// PairHandler serves pairing, to mount on the agent's TLS listener.
//
// It is not on the bootstrap server's own listener, which is cleartext because
// it hands out the certificate authority to a device that does not trust the
// agent's certificate yet. Pairing issues a durable credential and the key pin
// the device recognises the agent by, so it belongs on the listener already
// serving the certificate that pin covers.
func (s *BootstrapServer) PairHandler() http.Handler {
	return http.HandlerFunc(s.handlePair)
}

// handlePair issues a per-device credential to a caller that knows the PIN.
//
// The PIN is proof the operator can see the kiosk, and it is rate-limited and
// locks out after repeated failures, the same gate that protects the CA
// download, reused because it answers the same question.
//
// The response carries a durable device token and the key pin the device will
// recognise this agent by, so it is refused over a cleartext connection: an
// observer would read the credential, and an active attacker could substitute a
// pin of their own.
func (s *BootstrapServer) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Pairing requires POST.", http.StatusMethodNotAllowed)
		return
	}

	if !pairingChannelIsPrivate(r) {
		s.logger.Printf("Pairing refused for %s: the connection is not encrypted", r.RemoteAddr)
		http.Error(w, "Pairing requires HTTPS: a credential issued in the clear is not one.", http.StatusUpgradeRequired)
		return
	}

	s.pairMu.RLock()
	issuer, port := s.pairIssuer, s.agentPort
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
		AgentPort:    s.agentPortNow(port),
	})
}

// pairingChannelIsPrivate reports whether a credential may be issued over this
// connection. TLS qualifies. So does loopback, where there is no network to
// observe and an agent without a certificate can still pair its own machine.
func pairingChannelIsPrivate(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
