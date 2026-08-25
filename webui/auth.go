package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The control surface is privileged, so it has its own gate rather than reusing
// the client and device endpoints'. Three checks must pass: the request is from
// loopback, its Origin is the agent itself, and it carries a session token
// minted by the tray.
//
// The origin allowlist is deliberately not consulted. An entry there authorises
// a console to read tags; it must never confer the ability to revoke devices or
// rotate the secret.

const (
	cookieName = "davi_nfc_control"

	// A handoff token only has to survive the browser launching.
	handoffTTL = 2 * time.Minute

	sessionTTL = 12 * time.Hour
)

// Auth mints and verifies the credentials for the control surface.
// Handoff tokens are single-use: the tray puts one in the URL it opens and the
// console exchanges it for a session cookie, so a leaked URL is already spent.
type Auth struct {
	mu       sync.Mutex
	handoff  map[string]time.Time
	sessions map[string]time.Time
}

// NewAuth returns an empty credential store.
func NewAuth() *Auth {
	return &Auth{
		handoff:  make(map[string]time.Time),
		sessions: make(map[string]time.Time),
	}
}

// MintHandoff issues a single-use token for the tray to place in the console
// URL it opens.
func (a *Auth) MintHandoff() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()
	a.handoff[token] = time.Now().Add(handoffTTL)
	return token, nil
}

// RedeemHandoff exchanges a handoff token for a session token, consuming it.
// A token that is unknown, already redeemed or expired returns false.
func (a *Auth) RedeemHandoff(token string) (string, bool) {
	if token == "" {
		return "", false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()

	// Constant-time against every candidate rather than a map lookup, so the
	// time taken to reject cannot narrow down a valid token.
	var matched string
	for candidate := range a.handoff {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			matched = candidate
		}
	}
	if matched == "" {
		return "", false
	}
	delete(a.handoff, matched)

	session, err := randomToken()
	if err != nil {
		return "", false
	}
	a.sessions[session] = time.Now().Add(sessionTTL)
	return session, true
}

// ValidSession reports whether a session token is current.
func (a *Auth) ValidSession(token string) bool {
	if token == "" {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()

	for candidate := range a.sessions {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

// RevokeSession ends one session, used when the console signs out.
func (a *Auth) RevokeSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// RevokeAll ends every session and discards unclaimed tokens.
func (a *Auth) RevokeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handoff = make(map[string]time.Time)
	a.sessions = make(map[string]time.Time)
}

// SessionCount returns the number of live sessions, for display.
func (a *Auth) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()
	return len(a.sessions)
}

// expireLocked drops timed-out tokens. Caller holds the lock.
func (a *Auth) expireLocked() {
	now := time.Now()
	for token, expiry := range a.handoff {
		if now.After(expiry) {
			delete(a.handoff, token)
		}
	}
	for token, expiry := range a.sessions {
		if now.After(expiry) {
			delete(a.sessions, token)
		}
	}
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// isLoopbackRequest reports whether the request arrived over loopback. It reads
// RemoteAddr, never a forwarding header, since those are attacker-controlled.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// isSameOriginRequest reports whether the request came from a page this agent
// served. A request declaring no origin is accepted: that is a direct
// navigation or a non-browser client; what must be rejected is one declaring a
// different origin.
func isSameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	return strings.EqualFold(u.Host, r.Host)
}

// authorize returns the reason a request is refused, or "" if it
// is authorised.
func (a *Auth) authorize(r *http.Request) string {
	if !isLoopbackRequest(r) {
		return "not loopback"
	}
	if !isSameOriginRequest(r) {
		return "cross-origin"
	}

	cookie, err := r.Cookie(cookieName)
	if err != nil || !a.ValidSession(cookie.Value) {
		return "no valid session"
	}
	return ""
}
