package agent

import "net/http"

// Console is the control center as the agent sees it: two handlers for the
// unified server to mount, and a signal to redraw when something changes the
// agent from elsewhere.
//
// The agent deliberately knows no more than this. The implementation lives in
// agent/console, which reaches back into the agent through its own adapter —
// so the dependency runs one way and a -tags nowebui build drops the console
// without the agent noticing.
//
// A nil Console means there is none. Assign one only after a real nil check:
// a typed nil satisfies this interface and would defeat that check, the same
// hazard tokenVerifier and originPolicy guard against.
type Console interface {
	// Routes serves the privileged control API.
	Routes() http.Handler
	// Assets serves the embedded console frontend.
	Assets() http.Handler
	// NotifyChange asks the console to redraw.
	NotifyChange()
}

// consoleRoutes and consoleAssets are what the unified server mounts. Both
// tolerate the absence of a console.
func (a *Agent) consoleRoutes() http.Handler {
	if a.console == nil {
		return nil
	}
	return a.console.Routes()
}

func (a *Agent) consoleAssets() http.Handler {
	if a.console == nil {
		return nil
	}
	return a.console.Assets()
}
