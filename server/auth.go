package server

import (
	"crypto/subtle"
	"net"
	"net/http"
)

// IsLoopbackRequest returns true if r originates from a loopback IP
// (127.0.0.0/8 or ::1). Used to grant the kiosk's own frontend
// (running on localhost) access without requiring the API secret.
func IsLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// CheckAPISecret enforces the API secret on a WebSocket upgrade
// request. Loopback requests bypass the check (the kiosk frontend
// runs on localhost and shouldn't have to know the secret).
//
// Returns true if the request should be allowed to proceed; if it
// returns false the response has already been written. The expected
// secret is read from query (?secret=) and Authorization: Bearer
// header.
//
// If wantSecret is empty, no auth is performed (legacy mode).
func CheckAPISecret(w http.ResponseWriter, r *http.Request, wantSecret string) bool {
	if wantSecret == "" {
		return true
	}
	if IsLoopbackRequest(r) {
		return true
	}

	got := r.URL.Query().Get("secret")
	if got == "" {
		if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
			got = h[7:]
		}
	}

	if subtle.ConstantTimeCompare([]byte(got), []byte(wantSecret)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
