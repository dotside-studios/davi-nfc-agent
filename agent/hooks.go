package agent

// OnClientsChange registers fn to run when a client connects or disconnects, so
// a console can redraw without polling.
//
// Called on the connection's own goroutine, so it must not block.
func (a *Agent) OnClientsChange(fn func()) {
	if fn == nil {
		return
	}
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.clientHooks = append(a.clientHooks, fn)
}
