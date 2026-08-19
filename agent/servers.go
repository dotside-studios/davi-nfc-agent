package agent

import "context"

// serverStack is the agent's own HTTP surface: the bridge, the device and
// client handlers, and the single listener in front of them.
//
// It is a component like the pairing server, but unlike the pairing server it
// cannot be handed in by the caller. The listener is built from the device and
// client handlers, which are built from the reader the agent opens at Start, so
// it only exists once a run is underway. The agent therefore registers it
// itself, last -- which makes it the first thing to stop, so the listener stops
// accepting before the handlers behind it are taken apart.
type serverStack struct {
	agent *Agent
}

var _ Component = (*serverStack)(nil)

// Name identifies the component.
func (s *serverStack) Name() string { return "servers" }

// Start builds the bridge, the device and client handlers and the unified
// listener, and binds it.
func (s *serverStack) Start(ctx context.Context) error {
	return s.agent.startServers()
}

// Stop takes them down again, innermost last.
func (s *serverStack) Stop() error {
	s.agent.stopServers()
	return nil
}
