package serverplugin

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Authenticate returns the credential check for a device endpoint, to pass as
// remotenfc.ServerOptions.Authenticate. The device ID it returns names the
// paired device the credential belongs to, and is empty for the shared secret.
//
// It reads the agent's policy per request, so a rotated secret or a changed
// paired-device requirement takes effect without rebuilding the endpoint, and
// the check can be taken before the plugin activates. One taken from a plugin
// that never activates admits nobody.
func (p *Plugin) Authenticate() func(w http.ResponseWriter, r *http.Request) (deviceID string, ok bool) {
	return func(w http.ResponseWriter, r *http.Request) (string, bool) {
		a := p.agent
		if a == nil {
			authWarn.Printf("Connection rejected from %s: the server plugin has not activated", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return "", false
		}

		if a.RequirePairedDevice() {
			id, ok := server.CheckPairedDevice(w, r, a.TokenVerifier())
			if !ok {
				authWarn.Printf("Connection rejected from %s: no paired-device credential", r.RemoteAddr)
				return "", false
			}
			return id, true
		}

		id, ok := server.CheckAuth(w, r, a.APISecret(), a.TokenVerifier())
		if !ok {
			authWarn.Printf("WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
			return "", false
		}
		return id, true
	}
}
