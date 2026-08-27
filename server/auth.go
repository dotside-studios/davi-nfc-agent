package server

import (
	"crypto/subtle"
	"net"
	"net/http"
)

// IsLoopbackRequest reports whether r came from a loopback IP (127.0.0.0/8 or
// ::1), read from RemoteAddr. Forwarding headers are not consulted: the sender
// writes them.
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
// request. Loopback presents the secret like any other address; the bypass is
// [AuthOptions.AllowLoopback], which CheckAuth takes.
//
// Returns true if the request should be allowed to proceed; if it
// returns false the response has already been written. The expected
// secret is read from query (?secret=) and Authorization: Bearer
// header.
//
// If wantSecret is empty, no auth is performed (legacy mode).
func CheckAPISecret(w http.ResponseWriter, r *http.Request, wantSecret string) bool {
	_, ok := CheckAuth(w, r, AuthOptions{Secret: wantSecret})
	return ok
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

// AuthOptions are the credentials a connection may be admitted on.
type AuthOptions struct {
	// Secret is the shared API secret. Empty performs no check at all, which
	// is the development default.
	Secret string

	// Verifier recognizes per-device credentials issued at pairing. Nil for a
	// build that does not pair devices.
	Verifier TokenVerifier

	// AllowLoopback admits a request from 127.0.0.0/8 or ::1 with no
	// credential. Off by default: loopback identifies the host, so it also
	// admits other accounts on it, local proxies, and port forwards into it.
	AllowLoopback bool
}

// CheckAuth enforces credentials on a connection, accepting either a
// per-device token or the shared API secret.
//
// Returns true if the request should proceed; if false the response has
// already been written. The credential is read from query (?secret=) and
// Authorization: Bearer.
//
// deviceID names the paired device the credential belongs to, and is empty for
// an admission that identifies nobody: the shared secret, or the loopback
// bypass where AllowLoopback is set.
func CheckAuth(w http.ResponseWriter, r *http.Request, opts AuthOptions) (deviceID string, ok bool) {
	presented := presentedCredential(r)

	// A paired device is admitted on its own credential, whatever the shared
	// secret is set to, which is what makes per-device revocation meaningful.
	if opts.Verifier != nil && presented != "" {
		if id, ok := opts.Verifier.VerifyToken(presented); ok {
			return id, true
		}
	}

	if opts.Secret == "" {
		return "", true
	}
	if opts.AllowLoopback && IsLoopbackRequest(r) {
		return "", true
	}

	if subtle.ConstantTimeCompare([]byte(presented), []byte(opts.Secret)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return "", true
}

// CheckPairedDevice admits only a device holding a credential issued at
// pairing. Neither the shared secret nor the loopback bypass applies.
//
// This is the strict counterpart to CheckAuth, for device connections once the
// devices that matter have paired. It is deliberately not used for browser
// clients: a browser has no way to pair, and is gated by the origin allowlist
// instead.
func CheckPairedDevice(w http.ResponseWriter, r *http.Request, verifier TokenVerifier) (deviceID string, ok bool) {
	if verifier == nil {
		// Nothing can be verified, so nothing can be admitted. Failing closed
		// is the only safe reading of "require a paired device".
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	if id, ok := verifier.VerifyToken(presentedCredential(r)); ok {
		return id, true
	}

	http.Error(w, "Unauthorized", http.StatusUnauthorized)
	return "", false
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
