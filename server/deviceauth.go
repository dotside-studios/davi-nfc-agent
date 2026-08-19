package server

import (
	"log"
	"net/http"
	"sync/atomic"
)

// DeviceAuth admits or rejects a device connection before the driver behind it
// sees the request.
//
// The device driver speaks the device protocol and knows nothing about API
// secrets or pairing, so wrapping is the join: mount its handler behind this
// and the credential is checked once, in front, rather than by every driver.
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

// Wrap returns next behind the credential check.
func (a *DeviceAuth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.requirePaired.Load() {
			if !CheckPairedDevice(w, r, a.tokenVerifier) {
				log.Printf("[device] Connection rejected from %s: no paired-device credential", r.RemoteAddr)
				return
			}
		} else if !CheckAuth(w, r, a.apiSecret, a.tokenVerifier) {
			log.Printf("[device] WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
			return
		}

		next.ServeHTTP(w, r)
	})
}
