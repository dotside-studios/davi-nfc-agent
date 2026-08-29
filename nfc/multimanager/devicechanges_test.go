package multimanager

import (
	"testing"
	"time"
)

// notifyingManager is a manager whose device set changes on command, standing
// in for a driver like pcsc or remotenfc that implements DeviceChangeNotifier.
type notifyingManager struct {
	mockManager
	changes chan struct{}
}

func newNotifyingManager(name string) *notifyingManager {
	return &notifyingManager{mockManager: mockManager{name: name}, changes: make(chan struct{}, 1)}
}

func (m *notifyingManager) DeviceChanges() <-chan struct{} { return m.changes }

func (m *notifyingManager) notify() {
	select {
	case m.changes <- struct{}{}:
	default:
	}
}

func awaitChange(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not see the change within 2s", what)
	}
}

// DeviceChanges is called once by the agent's own watcher and once by the
// supervisor's, on the same MultiManager. A single shared channel let one
// steal the wakeup meant for both; each call must get its own.
func TestDeviceChangesReachesEveryCaller(t *testing.T) {
	child := newNotifyingManager("hardware")
	mm := NewMultiManager(ManagerEntry{Name: "hardware", Manager: child})
	defer mm.Close()

	first := mm.DeviceChanges()
	second := mm.DeviceChanges()

	child.notify()

	awaitChange(t, first, "the first caller")
	awaitChange(t, second, "the second caller")
}

// A watch started after another has already consumed the change still sees
// it: DeviceChanges is not a queue two callers compete to drain, it fans the
// same change out to whoever is watching when it happens.
func TestDeviceChangesDoesNotStarveALateCaller(t *testing.T) {
	child := newNotifyingManager("hardware")
	mm := NewMultiManager(ManagerEntry{Name: "hardware", Manager: child})
	defer mm.Close()

	early := mm.DeviceChanges()
	child.notify()
	awaitChange(t, early, "the early caller")

	late := mm.DeviceChanges()
	child.notify()
	awaitChange(t, late, "the late caller")
}

// Close ends every channel DeviceChanges has handed out, so a watcher's select
// sees a closed channel rather than blocking on one nothing will ever write to
// again.
func TestCloseEndsEveryDeviceChangesWatch(t *testing.T) {
	child := newNotifyingManager("hardware")
	mm := NewMultiManager(ManagerEntry{Name: "hardware", Manager: child})

	first := mm.DeviceChanges()
	second := mm.DeviceChanges()

	mm.Close()

	for name, ch := range map[string]<-chan struct{}{"first": first, "second": second} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("%s watch delivered a value instead of closing", name)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s watch was not closed by Close", name)
		}
	}
}

// A watch started after Close must not block forever either.
func TestDeviceChangesAfterCloseIsAlreadyClosed(t *testing.T) {
	child := newNotifyingManager("hardware")
	mm := NewMultiManager(ManagerEntry{Name: "hardware", Manager: child})
	mm.Close()

	ch := mm.DeviceChanges()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("a watch started after Close delivered a value")
		}
	case <-time.After(2 * time.Second):
		t.Error("a watch started after Close was not already closed")
	}
}
