package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// State is where the agent is in its lifecycle.
//
// Transitions are serialised: only one of Start or Stop runs at a time,
// whichever goroutine asks.
type State int32

const (
	// StateStopped is the state a newly built agent is in, and the one it
	// returns to after Stop.
	StateStopped State = iota

	// StateStarting is set while Start is opening the reader and binding the
	// listener. Nothing is serving yet.
	StateStarting

	// StateRunning means the reader is open and the servers are accepting.
	StateRunning

	// StateStopping is set while Stop is tearing the above back down.
	StateStopping
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

// Component is a part of the agent's runtime whose lifetime follows the
// agent's own. Register one with Use and the agent starts it once the readers
// are open, in registration order, and stops it in reverse before closing
// them.
//
// This is what the agent hangs anything extra from: a metrics exporter, a
// watchdog, a bridge to some other system. The context passed to Start is
// cancelled when the agent stops, so a component that only needs to know when
// to quit can watch that and let Stop be trivial.
type Component interface {
	// Name identifies the component in logs and in start-up errors.
	Name() string

	// Start begins the component's work. It should not block for the
	// component's lifetime: start goroutines and return. Returning an error
	// aborts the agent's start.
	Start(ctx context.Context) error

	// Stop ends it. It is called once, in reverse registration order, and is
	// expected to return promptly.
	Stop() error
}

// State reports where the agent is. Safe from any goroutine, including from a
// state hook.
func (a *Agent) State() State { return State(a.state.Load()) }

// Running reports whether the agent is serving. It is a snapshot: a caller
// deciding what to do next should ask the agent to do it and handle the error,
// rather than checking first.
func (a *Agent) Running() bool { return a.State() == StateRunning }

// Use registers a component to run alongside the agent. Components must be
// registered before Start; registering while the agent is running returns an
// error rather than silently never starting.
func (a *Agent) Use(c Component) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.useLocked(c)
}

// useLocked is Use with the lifecycle already held, which is how a plugin
// registers a component: activation runs under the same lock, so the public
// method would deadlock against itself.
func (a *Agent) useLocked(c Component) error {
	if c == nil {
		return fmt.Errorf("agent: nil component")
	}

	if State(a.state.Load()) != StateStopped {
		return fmt.Errorf("agent: cannot register component %q while %s", c.Name(), a.State())
	}
	for _, existing := range a.components {
		if existing.Name() == c.Name() {
			return fmt.Errorf("agent: component %q is already registered", c.Name())
		}
	}
	a.components = append(a.components, c)
	return nil
}

// Components lists the registered components in the order they start.
func (a *Agent) Components() []Component {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	out := make([]Component, len(a.components))
	copy(out, a.components)
	return out
}

// startComponents brings the registered components up in order. On the first
// failure it stops the ones already started and reports which failed.
func (a *Agent) startComponents(ctx context.Context) error {
	for i, c := range a.components {
		if err := c.Start(ctx); err != nil {
			a.logger.Printf("Component %q failed to start: %v", c.Name(), err)
			a.stopComponentRange(i - 1)
			return fmt.Errorf("agent: component %q: %w", c.Name(), err)
		}
		a.logger.Printf("Component started: %s", c.Name())
	}
	return nil
}

// stopComponentRange stops components from index last down to zero.
func (a *Agent) stopComponentRange(last int) {
	for i := last; i >= 0; i-- {
		c := a.components[i]
		if err := c.Stop(); err != nil {
			a.logger.Printf("Component %q failed to stop: %v", c.Name(), err)
		}
	}
}

// Start opens the readers, then starts the registered components, the
// listener among them. It is safe to call from any goroutine: a second caller either
// waits for the first to finish or is told the agent is already running.
func (a *Agent) Start(devicePath string) error {
	a.lifecycleMu.Lock()

	if State(a.state.Load()) != StateStopped {
		running := a.CurrentDevicePath()
		a.lifecycleMu.Unlock()
		if devicePath != "" && devicePath == running {
			a.logger.Printf("The readers are already running, on %s", devicePath)
			return nil
		}
		return fmt.Errorf("agent: cannot start while %s", a.State())
	}

	// The plugins go on before anything opens or binds: they are what mount
	// the routes and register the components this start brings up. A host with
	// a tray has already done this, with a menu to hang their entries on; this
	// is the fallback for one that has not, and for a headless run.
	if err := a.activateLocked(nil); err != nil {
		a.lifecycleMu.Unlock()
		return err
	}

	a.state.Store(int32(StateStarting))

	err := a.startLocked(devicePath)
	if err == nil {
		a.runCtx, a.runCancel = context.WithCancel(context.Background())
		err = a.startComponents(a.runCtx)
		if err != nil {
			a.runCancel()
			a.runCtx, a.runCancel = nil, nil
		}
	}

	if err != nil {
		// Never leave a half-started agent behind: whatever came up before the
		// failure comes back down, so the next Start begins from stopped.
		a.stopLocked()
		a.state.Store(int32(StateStopped))
		a.lifecycleMu.Unlock()
		a.fireState(StateStopped)
		return err
	}

	a.state.Store(int32(StateRunning))
	a.lifecycleMu.Unlock()
	a.fireState(StateRunning)
	return nil
}

// Stop takes the agent back down: components first, in reverse order, then the
// readers.
func (a *Agent) Stop() {
	a.lifecycleMu.Lock()

	if State(a.state.Load()) == StateStopped {
		a.lifecycleMu.Unlock()
		a.logger.Println("Agent is not running")
		return
	}

	a.state.Store(int32(StateStopping))

	if a.runCancel != nil {
		a.runCancel()
	}
	a.stopComponentRange(len(a.components) - 1)
	a.runCtx, a.runCancel = nil, nil

	a.stopLocked()

	a.state.Store(int32(StateStopped))
	a.lifecycleMu.Unlock()
	a.fireState(StateStopped)
}

// atomicState is the storage behind State. It is a separate type only so the
// zero Agent reads as stopped.
type atomicState = atomic.Int32

// lifecycle holds the fields the above needs. It is embedded in Agent.
type lifecycle struct {
	lifecycleMu sync.Mutex
	state       atomicState

	components []Component
	runCtx     context.Context
	runCancel  context.CancelFunc

	// activateErr is what activation decided, kept because it is decided once:
	// a start that follows a failed activation reports the same failure rather
	// than running the plugins again over the ones already registered. Guarded
	// by lifecycleMu, like everything else here.
	activateErr error
}
