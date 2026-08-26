package server

import (
	"log"
	"net/http"
	"sync/atomic"
)

// DeviceAuth admits or rejects a device connection before the driver serves it.
//
// The driver speaks the device protocol and knows nothing about API secrets or
// pairing; it asks for an authenticator and this is the agent's. Passed as
// remotenfc.ServerOptions.Authenticate, so the check lives inside the endpoint
// rather than depending on whoever mounts it remembering to wrap it.
type DeviceAuth struct {
	apiSecret     string
	tokenVerifier TokenVerifier

	// requirePaired is read on every upgrade and settable while the agent runs,
	// so the policy can be tried without restarting the listener.
	requirePaired atomic.Bool
}

// NewDeviceAuth builds the gate. An empty apiSecret means no shared secret is
// required, which is the development default.
func NewDeviceAuth(apiSecret string, verifier TokenVerifier, requirePaired bool) *DeviceAuth {
	a := &DeviceAuth{apiSecret: apiSecret, tokenVerifier: verifier}
	a.requirePaired.Store(requirePaired)
	return a
}

// SetRequirePaired turns the paired-device requirement on or off.
func (a *DeviceAuth) SetRequirePaired(on bool) { a.requirePaired.Store(on) }

// RequirePaired reports whether only paired devices are admitted.
func (a *DeviceAuth) RequirePaired() bool { return a.requirePaired.Load() }

// Check admits or rejects one request, writing the rejection itself. This is
// the form a driver takes, so the credential is checked inside the endpoint
// rather than only by whoever remembered to wrap it.
//
// It names the paired device it admitted, so the driver registers it under the
// identity it paired with rather than minting one per connection.
func (a *DeviceAuth) Check(w http.ResponseWriter, r *http.Request) (deviceID string, ok bool) {
	if a.requirePaired.Load() {
		id, ok := CheckPairedDevice(w, r, a.tokenVerifier)
		if !ok {
			log.Printf("[device] Connection rejected from %s: no paired-device credential", r.RemoteAddr)
			return "", false
		}
		return id, true
	}

	id, ok := CheckAuth(w, r, a.apiSecret, a.tokenVerifier)
	if !ok {
		log.Printf("[device] WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
		return "", false
	}
	return id, true
}
