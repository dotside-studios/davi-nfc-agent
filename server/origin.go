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
//     is allowed. The WebSocket Origin guard exists to stop browsers
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

	return checkOrigin(func(host string) bool {
		if allowAny {
			return true
		}
		_, ok := allowed[host]
		return ok
	}, nil)
}

// OriginPolicy decides whether a browser origin may connect, and is told when
// one is refused so the caller can surface it. A rejected upgrade is otherwise
// indistinguishable to the operator from a stopped agent.
type OriginPolicy interface {
	Allowed(origin string) bool
	RecordBlocked(origin string)
}

// CheckOriginPolicy returns an origin checker backed by a policy. The empty and
// same-host cases behave exactly as CheckOrigin; the policy decides the rest.
func CheckOriginPolicy(policy OriginPolicy) func(r *http.Request) bool {
	if policy == nil {
		return CheckOrigin(nil)
	}
	return checkOrigin(policy.Allowed, policy.RecordBlocked)
}

// checkOrigin holds the rules shared by both constructors: no Origin means a
// native client, same-host is always fine, and anything else is the caller's
// decision.
func checkOrigin(allowed func(host string) bool, onReject func(origin string)) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// No Origin header, almost certainly a native client.
			// Browser-initiated WebSockets always send Origin.
			return true
		}
		return grantOrigin(origin, r.Host, allowed, onReject)
	}
}

// grantOrigin is the shared same-host + policy decision for a non-empty browser
// Origin against the Host it was sent to, used by both the WebSocket origin
// check and the HTTP CORS middleware so the two never diverge. It reports
// whether the origin is granted; allowed may be nil (only same-host is granted
// then), and a malformed or refused origin is reported to onReject when set.
func grantOrigin(origin, host string, allowed func(host string) bool, onReject func(origin string)) bool {
	u, err := url.Parse(origin)
	if err != nil {
		if onReject != nil {
			onReject(origin)
		}
		return false
	}

	// Same-host: the Origin's host:port matches the Host the request was made
	// to. Covers a kiosk frontend hitting itself.
	if hostsEqual(u.Host, host) {
		return true
	}

	if allowed != nil && allowed(u.Host) {
		return true
	}

	if onReject != nil {
		onReject(u.Host)
	}
	return false
}

// hostsEqual compares two host:port strings, case-folding the
// hostname. Both sides are expected to include the port, since the agent
// always binds to a non-standard port, so a missing port in either
// header is unusual and we don't try to default-port it (which would
// risk false positives on different explicit ports).
func hostsEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}
