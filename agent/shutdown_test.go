package agent

import (
	"sync/atomic"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// closableManager counts the closes an agent asks of its manager. It embeds a
// mock, so it behaves as a manager in every other respect.
type closableManager struct {
	*nfc.MockManager
	closes atomic.Int32
}

func (m *closableManager) Close() { m.closes.Add(1) }

func agentOver(t *testing.T, m nfc.Manager, port int) *Agent {
	t.Helper()

	opts := testOptions(t)
	opts.DevicePort = port
	opts.DevicePortSet = true
	rt, err := Setup(opts, m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return rt.Agent
}

// TestStopLeavesTheManagerOpen is the invariant behind Stop and Shutdown being
// separate calls. The manager is built once for the process, and the tray and
// the console both stop the agent expecting to start it again — closing it on
// the way down leaves the restart with a manager that is already shut.
func TestStopLeavesTheManagerOpen(t *testing.T) {
	m := &closableManager{MockManager: nfc.NewMockManager()}
	a := agentOver(t, m, 9481)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	a.Stop()

	if got := m.closes.Load(); got != 0 {
		t.Fatalf("Stop closed the manager %d times; it must survive a stop", got)
	}

	// The restart the tray's device switch and the console's stop button rely on.
	if err := a.Start(""); err != nil {
		t.Fatalf("restart after Stop: %v", err)
	}
	a.Stop()
}

// TestShutdownClosesTheManager covers the other half: the terminal path does
// release it, so quitting does not leak the manager's goroutines.
func TestShutdownClosesTheManager(t *testing.T) {
	m := &closableManager{MockManager: nfc.NewMockManager()}
	a := agentOver(t, m, 9482)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	a.Shutdown()

	if got := m.closes.Load(); got != 1 {
		t.Errorf("manager closed %d times after Shutdown, want 1", got)
	}
	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s after Shutdown, want stopped", got)
	}
}

// TestShutdownWithoutCloserIsHarmless: most managers have no Close at all, and
// Shutdown must not require one.
func TestShutdownWithoutCloserIsHarmless(t *testing.T) {
	a := agentOver(t, nfc.NewMockManager(), 9483)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	a.Shutdown()

	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s after Shutdown, want stopped", got)
	}
}
