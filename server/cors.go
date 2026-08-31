package server

import "net/http"

// CORS wraps a handler in the permissive headers a browser needs to call it
// from another origin, answering a preflight itself.
//
// Applied per route rather than to everything, because the agent's routes are
// not alike: the client endpoint and the health checks are called by web apps,
// while the control API administers the agent and the console is a page. Those
// last two are deliberately left bare, so no other origin can call them and
// read the reply.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", CORSAllowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", CORSAllowMethods)
		w.Header().Set("Access-Control-Allow-Headers", CORSAllowHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// CORSPolicy is [CORS] scoped to an origin policy instead of "*": it echoes the
// request's Origin only when the policy grants it — same-host, or on the
// allowlist — rather than allowing every origin to read the reply. The reply
// carries Vary: Origin so a cache keys on the origin it was scoped to, and a
// preflight is answered here.
//
// It is the same decision the WebSocket origin check makes (see
// [CheckOriginPolicy]), so an endpoint that admits a browser over both agrees
// with itself, and a refused origin is recorded through the policy exactly as a
// refused upgrade is — so the operator sees it and can allow it. A nil policy
// grants only same-host.
//
// next always runs: a CORS header, not the handler, is what lets a browser read
// a cross-origin reply, so refusing the header is the whole enforcement. An
// endpoint that must also refuse the work — a 403 with no body — checks the
// origin in its own handler; this middleware does not stand in for that.
func CORSPolicy(policy OriginPolicy, next http.Handler) http.Handler {
	var allowed func(string) bool
	var onReject func(string)
	if policy != nil {
		allowed = policy.Allowed
		onReject = policy.RecordBlocked
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			// The reply's CORS headers depend on the Origin whether or not one
			// is granted, so a cache must key on it either way.
			w.Header().Add("Vary", "Origin")
			if grantOrigin(origin, r.Host, allowed, onReject) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", CORSAllowMethods)
				w.Header().Set("Access-Control-Allow-Headers", CORSAllowHeaders)
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
