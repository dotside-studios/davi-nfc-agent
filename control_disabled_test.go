//go:build nocontrol

package main

import "testing"

// The point of this build is that the control surface is absent, so these are
// the contract: a nil handler means the unified server mounts nothing, and the
// root falls back to the banner. If someone reintroduces a dependency the
// package will simply stop compiling under this tag, which is the real guard —
// these assertions cover the part that would otherwise compile but misbehave.
func TestControlIsAbsent(t *testing.T) {
	if got := setupControlCenter(nil, nil, nil, nil, 0); got != nil {
		t.Errorf("setupControlCenter returned %v, want nil", got)
	}

	var control *ControlServer
	if h := control.Handler(); h != nil {
		t.Error("Handler returned routes in a nocontrol build")
	}
	// Called by the origin and device stores on every change; must tolerate nil.
	control.NotifyChange()

	if h := webUIHandler(); h != nil {
		t.Error("webUIHandler returned a handler in a nocontrol build")
	}
}
