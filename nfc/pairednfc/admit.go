package pairednfc

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceid"
)

// Admit wraps a backend's device endpoint with this manager's credential check.
//
// The identity a device is admitted under travels on the request, so the
// backend registers it under the identity it paired with rather than one of its
// own minting. See [deviceid].
//
// Wrapping is per endpoint, at the mount, and is not implied by sitting in this
// manager's tree: a reader attached to this machine serves no endpoint and is
// not gated. Mounting an endpoint unwrapped serves devices with no credential
// check at all.
func (m *Manager) Admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := m.authenticate(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, deviceid.With(r, id))
	})
}

// authenticate admits or refuses a request, writing its own response when it
// refuses, and names the device it admitted.
//
// The policy is read here rather than captured, so rotating the secret or
// withdrawing the requirement takes effect on the next connection.
func (m *Manager) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	if m.policy.requirePaired() {
		// Neither the shared secret nor the loopback bypass applies. An empty
		// registry admits nobody, including on the machine that has just
		// revoked its last device.
		id, ok := server.CheckPairedDevice(w, r, m.registry)
		if !ok {
			pairedWarn.Printf("Connection refused from %s: no paired-device credential", r.RemoteAddr)
			return "", false
		}
		return id, true
	}

	// A paired credential, the shared secret, or the loopback bypass. The
	// secret is a peer of a paired token rather than a replacement: a paired
	// device is admitted on its own credential whatever the secret is set to,
	// which is what makes revoking one device meaningful.
	id, ok := server.CheckAuth(w, r, m.policy.secret(), m.registry)
	if !ok {
		pairedWarn.Printf("Connection refused from %s: bad or missing credential", r.RemoteAddr)
		return "", false
	}
	return id, true
}
