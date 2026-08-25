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
