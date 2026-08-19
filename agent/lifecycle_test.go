package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func runningAgent(t *testing.T, port int) *Agent {
	t.Helper()

	opts := testOptions(t)
	opts.DevicePort = port
	opts.DevicePortSet = true
	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return rt.Agent
}

// TestConcurrentLifecycle is the reason the lock exists. The tray, the console
// and the network watcher all reach Start and Stop from different goroutines,
// and before this they raced on Reader and the server fields.
func TestConcurrentLifecycle(t *testing.T) {
	a := runningAgent(t, 9467)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = a.Start("") }()
		go func() { defer wg.Done(); a.Stop() }()
	}
	wg.Wait()

	a.Stop()
	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s after the dust settles, want stopped", got)
	}
}

// TestStateTransitions pins what a hook observes.
func TestStateTransitions(t *testing.T) {
	a := runningAgent(t, 9468)

	var mu sync.Mutex
	var seen []State
	a.OnStateChange(func(s State) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, s)
	})

	if got := a.State(); got != StateStopped {
		t.Fatalf("a fresh agent is %s, want stopped", got)
	}
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := a.State(); got != StateRunning {
		t.Errorf("State() = %s after Start, want running", got)
	}
	if !a.Running() {
		t.Error("Running() should be true after Start")
	}
	a.Stop()
	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s after Stop, want stopped", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != StateRunning || seen[1] != StateStopped {
		t.Errorf("hook saw %v, want [running stopped]", seen)
	}
}

// TestRepeatStartAndStop keeps the old semantics: starting the reader already
// open is not an error, and stopping a stopped agent is a no-op.
func TestRepeatStartAndStop(t *testing.T) {
	a := runningAgent(t, 9469)

	if err := a.Start(""); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// "" resolved to a reader; asking for that same reader again is a no-op,
	// which is the long-standing behaviour the tray relies on.
	if err := a.Start(a.CurrentDevicePath()); err != nil {
		t.Errorf("starting the same device again = %v, want nil", err)
	}
	if err := a.Start("some-other-reader"); err == nil {
		t.Error("starting a different device while running should be refused")
	}
	a.Stop()
	a.Stop() // must not panic or change state
	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s, want stopped", got)
	}
}

// --- components ---

type probe struct {
	name    string
	failOn  bool
	mu      sync.Mutex
	started bool
	stopped bool
	order   *[]string
}

func (p *probe) Name() string { return p.name }

func (p *probe) Start(ctx context.Context) error {
	if p.failOn {
		return errors.New("refused")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = true
	*p.order = append(*p.order, "start:"+p.name)
	return nil
}

func (p *probe) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	*p.order = append(*p.order, "stop:"+p.name)
	return nil
}

// TestComponentsFollowTheAgent is the hook point: register, and the agent runs
// it for exactly as long as it runs itself.
func TestComponentsFollowTheAgent(t *testing.T) {
	a := runningAgent(t, 9470)

	var order []string
	one := &probe{name: "one", order: &order}
	two := &probe{name: "two", order: &order}

	if err := a.Use(one); err != nil {
		t.Fatalf("Use(one): %v", err)
	}
	if err := a.Use(two); err != nil {
		t.Fatalf("Use(two): %v", err)
	}

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !one.started || !two.started {
		t.Fatal("components did not start with the agent")
	}
	a.Stop()
	if !one.stopped || !two.stopped {
		t.Fatal("components did not stop with the agent")
	}

	want := []string{"start:one", "start:two", "stop:two", "stop:one"}
	if len(order) != 4 {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v (stop is reverse of start)", order, want)
		}
	}
}

// TestComponentFailureAbortsStart: a component that will not start leaves the
// agent stopped rather than half up.
func TestComponentFailureAbortsStart(t *testing.T) {
	a := runningAgent(t, 9471)

	var order []string
	good := &probe{name: "good", order: &order}
	bad := &probe{name: "bad", failOn: true, order: &order}

	_ = a.Use(good)
	_ = a.Use(bad)

	err := a.Start("")
	if err == nil {
		t.Fatal("Start should fail when a component refuses")
	}
	if got := a.State(); got != StateStopped {
		t.Errorf("State() = %s after a failed Start, want stopped", got)
	}
	if !good.stopped {
		t.Error("a component started before the failure should be stopped again")
	}
	if a.Reader != nil {
		t.Error("a failed Start must not leave a reader open")
	}
}

// TestUseRejectedWhileRunning is the trap the console's attach mechanism fell
// into: registering something after Start silently never ran it.
func TestUseRejectedWhileRunning(t *testing.T) {
	a := runningAgent(t, 9472)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	var order []string
	if err := a.Use(&probe{name: "late", order: &order}); err == nil {
		t.Error("Use while running should report the problem, not accept and ignore")
	}
}

// TestDuplicateComponentRejected keeps two registrations of one name from both
// appearing to be installed.
func TestDuplicateComponentRejected(t *testing.T) {
	a := runningAgent(t, 9473)

	var order []string
	if err := a.Use(&probe{name: "dup", order: &order}); err != nil {
		t.Fatal(err)
	}
	if err := a.Use(&probe{name: "dup", order: &order}); err == nil {
		t.Error("a second component with the same name should be rejected")
	}
	if n := len(a.Components()); n != 1 {
		t.Errorf("Components() = %d, want 1", n)
	}
	_ = time.Now
}
