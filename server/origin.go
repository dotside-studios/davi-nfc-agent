package server

import (
	"net/http"
	"net/url"
	"strings"
)

// CheckOrigin returns a websocket Upgrader-compatible origin checker.
//
// Policy:
//   - Empty Origin header (typical for native mobile apps and curl)
//     is allowed — the WebSocket Origin guard exists to stop browsers
//     from being weaponized against localhost-bound services, and
//     non-browser clients don't send it.
//   - Same-host Origin (Origin host == Host header) is allowed.
//   - Origins listed in extraAllowed are allowed (use cases: a separate
//     dashboard origin, an admin tool).
//   - Anything else is rejected, which prevents a malicious website
//     from upgrading a WebSocket against this agent's port via a
//     victim browser (CSWSH).
//
// extraAllowed entries are matched against the Origin's host:port. Use
// "*" as a single entry to disable origin checking entirely (legacy
// behaviour, NOT recommended).
func CheckOrigin(extraAllowed []string) func(r *http.Request) bool {
	allowAny := false
	allowed := make(map[string]struct{}, len(extraAllowed))
	for _, e := range extraAllowed {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if e == "*" {
			allowAny = true
			continue
		}
		allowed[e] = struct{}{}
	}

	return func(r *http.Request) bool {
		if allowAny {
			return true
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			// No Origin header — almost certainly a native client.
			// Browser-initiated WebSockets always send Origin.
			return true
		}

		u, err := url.Parse(origin)
		if err != nil {
			return false
		}

		// Same-host: the Origin's host:port matches the Host the
		// request was made to. Covers kiosk frontend hitting itself.
		if hostsEqual(u.Host, r.Host) {
			return true
		}

		// Allowlist match.
		if _, ok := allowed[u.Host]; ok {
			return true
		}
		return false
	}
}

// hostsEqual compares two host:port strings, case-folding the
// hostname. Both sides are expected to include the port — the agent
// always binds to a non-standard port, so a missing port in either
// header is unusual and we don't try to default-port it (which would
// risk false positives on different explicit ports).
func hostsEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}
