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
	return CheckAuth(w, r, wantSecret, nil)
}

// TokenVerifier recognizes per-device credentials issued at pairing.
//
// It exists so a device can be revoked on its own: the shared secret is
// all-or-nothing, and rotating it to remove one phone logs out every other
// device at the same time.
type TokenVerifier interface {
	// VerifyToken reports whether a presented token belongs to a paired
	// device, returning its ID for logging.
	VerifyToken(token string) (deviceID string, ok bool)
}

// CheckAuth enforces credentials on a connection, accepting either a
// per-device token or the shared API secret.
//
// Returns true if the request should proceed; if false the response has
// already been written. The credential is read from query (?secret=) and
// Authorization: Bearer.
func CheckAuth(w http.ResponseWriter, r *http.Request, wantSecret string, verifier TokenVerifier) bool {
	presented := presentedCredential(r)

	// A paired device is admitted on its own credential, whatever the shared
	// secret is set to — that is what makes per-device revocation meaningful.
	if verifier != nil && presented != "" {
		if _, ok := verifier.VerifyToken(presented); ok {
			return true
		}
	}

	if wantSecret == "" {
		return true
	}
	if IsLoopbackRequest(r) {
		return true
	}

	if subtle.ConstantTimeCompare([]byte(presented), []byte(wantSecret)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// presentedCredential extracts the credential from the query string or an
// Authorization header.
func presentedCredential(r *http.Request) string {
	if got := r.URL.Query().Get("secret"); got != "" {
		return got
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}
