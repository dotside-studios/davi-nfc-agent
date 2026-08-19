package server

import "net/http"

// RouteByMode sends a request to the handler for the mode it declares, falling
// back to def when it declares none or names one nothing is mounted for.
//
// The device and client endpoints share one path, so the mode is what tells
// them apart. A request that names nothing is a client: browsers are the older
// half of the protocol and say nothing, so the fallback is not a guess.
func RouteByMode(def http.Handler, byMode map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := byMode[requestMode(r)]; ok && h != nil {
			h.ServeHTTP(w, r)
			return
		}
		def.ServeHTTP(w, r)
	})
}

// requestMode reads the mode a request declares.
//
// Devices announce themselves two ways: ?mode=device, which names the mode, and
// X-Device-Mode: true, which is a boolean predating it. The header is mapped
// onto the name it means so both arrive here as one value.
func requestMode(r *http.Request) string {
	if r.Header.Get("X-Device-Mode") == "true" {
		return ModeDevice
	}
	return r.URL.Query().Get("mode")
}

// ModeDevice is the mode a device declares.
const ModeDevice = "device"
