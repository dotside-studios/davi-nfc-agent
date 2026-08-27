package pairednfc

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/deviceid"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Admit wraps a backend's device endpoint with this manager's credential check.
//
// The identity a device is admitted under travels on the request, so the
// backend registers it under the identity it paired with rather than one of the
// backend's own minting. The backend never learns what a credential is; see
// [deviceid].
//
// Wrapping is per endpoint, at the mount, and never implied by sitting in this
// manager's tree. A reader attached to this machine is beneath this manager and
// is not gated by it, because there is no endpoint to wrap and no identity to
// check. Leaving an endpoint unwrapped is how a build serves devices without
// credentials at all.
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
// withdrawing the paired-device requirement takes effect on the next connection
// without anything being rebuilt.
func (m *Manager) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	if m.policy.requirePaired() {
		// Strict: neither the shared secret nor the loopback bypass applies. An
		// empty registry admits nobody, which is the only safe reading of
		// "require a paired device" — including for the machine that has just
		// revoked its last one.
		id, ok := server.CheckPairedDevice(w, r, m.registry)
		if !ok {
			pairedWarn.Printf("Connection refused from %s: no paired-device credential", r.RemoteAddr)
			return "", false
		}
		return id, true
	}

	// A paired credential, the shared secret, or the loopback bypass. The
	// secret is a peer of a paired token rather than a replacement: a device
	// that paired is admitted on its own credential whatever the secret is set
	// to, which is what makes revoking one device meaningful.
	id, ok := server.CheckAuth(w, r, m.policy.secret(), m.registry)
	if !ok {
		pairedWarn.Printf("Connection refused from %s: bad or missing credential", r.RemoteAddr)
		return "", false
	}
	return id, true
}
