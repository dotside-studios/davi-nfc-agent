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

// OnPreferencesChange registers fn to run when a preference changes, whoever
// changed it, so a surface showing one redraws without polling. See
// [Agent.Preferences] for what counts.
//
// Called on the goroutine that made the change, so it must not block.
func (a *Agent) OnPreferencesChange(fn func()) {
	if fn == nil {
		return
	}
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.preferenceHooks = append(a.preferenceHooks, fn)
}

// firePreferencesChanged runs the preference hooks, in registration order.
func (a *Agent) firePreferencesChanged() {
	a.hooksMu.Lock()
	hooks := make([]func(), len(a.preferenceHooks))
	copy(hooks, a.preferenceHooks)
	a.hooksMu.Unlock()

	for _, fn := range hooks {
		fn()
	}
}

// OnServerRestart registers fn to run after the listeners have been rebuilt,
// which is when the addresses a menu or a page shows can have changed.
//
// A hook rather than a read of ServerRestarts, so more than one thing can
// follow a restart: that channel has a single consumer, and a second reader
// would take the signal from the first.
func (a *Agent) OnServerRestart(fn func()) {
	if fn == nil {
		return
	}
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.restartHooks = append(a.restartHooks, fn)
}

// fireServerRestart runs the restart hooks, in registration order.
func (a *Agent) fireServerRestart() {
	a.hooksMu.Lock()
	hooks := make([]func(), len(a.restartHooks))
	copy(hooks, a.restartHooks)
	a.hooksMu.Unlock()

	for _, fn := range hooks {
		fn()
	}
}
