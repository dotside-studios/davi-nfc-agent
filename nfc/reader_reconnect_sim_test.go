package nfc

import (
	"errors"
	"testing"
	"time"
)

// twoLaneFailingManager offers two pollable readers but refuses every OpenDevice,
// so a reconnection attempt always fails — the condition under which a reader's
// device path is cleared after an error.
type twoLaneFailingManager struct {
	paths []string
}

func (m *twoLaneFailingManager) OpenDevice(string) (Device, error) {
	return nil, errors.New("i/o error: device unavailable")
}

func (m *twoLaneFailingManager) Devices() ([]DeviceListing, error) {
	out := make([]DeviceListing, 0, len(m.paths))
	for _, p := range m.paths {
		out = append(out, DeviceListing{Path: p, ID: p, Capabilities: DeviceCapabilities{CanPoll: true}})
	}
	return out, nil
}

// pumpClock advances a FakeClock in the background so the reconnect waits inside
// HandleError elapse without the test sleeping in real time. Returns a stop func.
func pumpClock(clock *FakeClock) func() {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				clock.Advance(500 * time.Millisecond)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	return func() { close(done) }
}

// TestReconnect_ClearedPathReturnsToAssignedDevice is the multi-lane reconnection
// worst case: a reader driving one lane hits a device error it cannot reconnect
// through, so its path is cleared. On a host with several readers the reader must
// return to reconnecting ITS OWN lane — it must not fall back to auto-discovering
// and adopt a different lane's reader, which would silently start driving the
// wrong tag. Here the erroring reader owns "gate" while "aisle" sorts first, so an
// auto-discovery fallback would grab "aisle".
func TestReconnect_ClearedPathReturnsToAssignedDevice(t *testing.T) {
	clock := NewFakeClock(time.Now())
	mgr := &twoLaneFailingManager{paths: []string{"aisle", "gate"}}
	dm := NewDeviceManager(mgr, "gate", clock)

	stopPump := pumpClock(clock)
	defer stopPump()

	// An I/O error the reader cannot reconnect through clears the path.
	dm.HandleError(errors.New("i/o error: reader vanished"), make(chan struct{}))

	if got := dm.DevicePath(); got != "gate" {
		t.Errorf("after a path-clearing error, the reader's path = %q; it must return to its own lane %q, not fall back to auto-discovering another reader", got, "gate")
	}
}

// TestReconnect_NoAssignedPathStillAutoDiscovers guards the other half of the
// contract: a reader created with no assigned lane (the standalone "use whatever
// reader is present" mode) must still clear to empty so it can auto-discover.
func TestReconnect_NoAssignedPathStillAutoDiscovers(t *testing.T) {
	clock := NewFakeClock(time.Now())
	mgr := &twoLaneFailingManager{paths: []string{"aisle", "gate"}}
	dm := NewDeviceManager(mgr, "", clock)

	stopPump := pumpClock(clock)
	defer stopPump()

	dm.HandleError(errors.New("i/o error: reader vanished"), make(chan struct{}))

	if got := dm.DevicePath(); got != "" {
		t.Errorf("a reader with no assigned lane cleared to %q; it must clear to empty so auto-discovery can pick a reader", got)
	}
}
