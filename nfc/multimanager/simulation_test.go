package multimanager

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// These simulations exercise the aggregate that fronts every backend at once:
// hardware readers and phones feeding one agent. The behaviours only a multi-lane
// arrangement can exhibit are the target — an operation reaching the one backend
// that holds its tag and no other, scans from every backend arriving on one
// signal, a device-change storm fanning out to every watcher without a lost
// wakeup or a deadlock, and one backend flapping without hiding the rest. The
// concurrency tests are meant to be run under -race.

// syncHoldingManager is a holdingManager whose bookkeeping is safe to touch from
// several goroutines, for the concurrent routing simulation.
type syncHoldingManager struct {
	mockManager
	mu      sync.Mutex
	holding map[string]string
	writes  map[string]int
}

func newSyncHoldingManager(name string, holding map[string]string) *syncHoldingManager {
	return &syncHoldingManager{
		mockManager: mockManager{name: name},
		holding:     holding,
		writes:      map[string]int{},
	}
}

func (m *syncHoldingManager) TagOn(deviceID string) (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deviceID == "" {
		for id, uid := range m.holding {
			return id, uid, true
		}
		return "", "", false
	}
	uid, ok := m.holding[deviceID]
	if !ok {
		return "", "", false
	}
	return deviceID, uid, true
}

func (m *syncHoldingManager) DevicesHoldingTags() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.holding))
	for id := range m.holding {
		out = append(out, id)
	}
	return out
}

func (m *syncHoldingManager) WriteTag(_ context.Context, deviceID, tagUID string, _ *nfc.NDEFMessage, lock bool, _ string) (*nfc.WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.holding[deviceID]; !ok {
		return nil, fmt.Errorf("device %s is not holding a tag on %s", deviceID, m.name)
	}
	m.writes[deviceID]++
	return &nfc.WriteResult{UID: tagUID, Locked: lock}, nil
}

func (m *syncHoldingManager) LockTag(_ context.Context, deviceID, tagUID, _ string) (*nfc.LockResult, error) {
	return &nfc.LockResult{UID: tagUID, Locked: true}, nil
}

func (m *syncHoldingManager) TagCapabilities(_ context.Context, deviceID, tagUID string) (*nfc.TagCapabilities, error) {
	caps := nfc.GetTagCapabilities(nfc.NewMockTag(tagUID))
	return &caps, nil
}

func (m *syncHoldingManager) TransceiveTag(_ context.Context, _, _ string, data []byte, _ bool) ([]byte, error) {
	return data, nil
}

func (m *syncHoldingManager) writeCount(deviceID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes[deviceID]
}

// scanningManager reports scans of its own, like the phone backend, so scans from
// several backends can be shown to fan in on the aggregate's one signal.
type scanningManager struct {
	mockManager
	scans event.Signal[nfc.ScannedTag]
}

func (m *scanningManager) Scans() *event.Signal[nfc.ScannedTag] { return &m.scans }

// TestSim_WriteRoutesToTheHolderThatHasTheTag: with two phone backends each
// holding a different device's tag, a write must reach the backend that holds the
// named device and never the other. Routing a write to the wrong backend would
// encode onto the wrong tag — the multi-lane version of a cross-tap.
func TestSim_WriteRoutesToTheHolderThatHasTheTag(t *testing.T) {
	east := newSyncHoldingManager("phones-east", map[string]string{"phone-east": "04EA57"})
	west := newSyncHoldingManager("phones-west", map[string]string{"phone-west": "04WE57"})

	mm := NewMultiManager(
		ManagerEntry{Name: "phones-east", Manager: east},
		ManagerEntry{Name: "phones-west", Manager: west},
	)
	defer mm.Close()

	if _, err := mm.WriteTag(context.Background(), "phone-west", "04WE57", nfc.NewNDEFMessage(), false, "k"); err != nil {
		t.Fatalf("write to phone-west: %v", err)
	}
	if east.writeCount("phone-east") != 0 || east.writeCount("phone-west") != 0 {
		t.Error("the east backend, which does not hold the named device, was written to")
	}
	if west.writeCount("phone-west") != 1 {
		t.Errorf("the west backend should have taken exactly one write, took %d", west.writeCount("phone-west"))
	}

	// A device no backend holds is refused, not misrouted.
	if _, err := mm.WriteTag(context.Background(), "phone-ghost", "04G057", nfc.NewNDEFMessage(), false, "k"); err == nil {
		t.Error("a write to a device no backend holds should be refused")
	}
}

// TestSim_ConcurrentWritesAcrossBackends fires writes at two backends at once and
// checks each backend received exactly the writes meant for it — routing must
// stay correct under concurrent load, not just serially.
func TestSim_ConcurrentWritesAcrossBackends(t *testing.T) {
	east := newSyncHoldingManager("phones-east", map[string]string{"phone-east": "04EA57"})
	west := newSyncHoldingManager("phones-west", map[string]string{"phone-west": "04WE57"})
	mm := NewMultiManager(
		ManagerEntry{Name: "phones-east", Manager: east},
		ManagerEntry{Name: "phones-west", Manager: west},
	)
	defer mm.Close()

	const perLane = 50
	var wg sync.WaitGroup
	for _, dev := range []string{"phone-east", "phone-west"} {
		wg.Add(1)
		go func(deviceID string) {
			defer wg.Done()
			for i := 0; i < perLane; i++ {
				_, _ = mm.WriteTag(context.Background(), deviceID, "uid", nfc.NewNDEFMessage(), false, "k")
			}
		}(dev)
	}
	wg.Wait()

	if got := east.writeCount("phone-east"); got != perLane {
		t.Errorf("east took %d writes, want %d", got, perLane)
	}
	if got := west.writeCount("phone-west"); got != perLane {
		t.Errorf("west took %d writes, want %d", got, perLane)
	}
	if east.writeCount("phone-west") != 0 || west.writeCount("phone-east") != 0 {
		t.Error("a write leaked across backends under concurrent load")
	}
}

// TestSim_ScansFanInFromEveryBackend: scans emitted by several backends at once
// must all surface on the aggregate's single Scans signal, none dropped. This is
// the read side of multi-lane — one client stream carrying every reader's taps.
func TestSim_ScansFanInFromEveryBackend(t *testing.T) {
	a := &scanningManager{mockManager: mockManager{name: "a"}}
	b := &scanningManager{mockManager: mockManager{name: "b"}}
	c := &scanningManager{mockManager: mockManager{name: "c"}}
	mm := NewMultiManager(
		ManagerEntry{Name: "a", Manager: a},
		ManagerEntry{Name: "b", Manager: b},
		ManagerEntry{Name: "c", Manager: c},
	)
	defer mm.Close()

	const perBackend = 100
	var mu sync.Mutex
	seen := map[string]int{}
	conn := mm.Scans().Connect(func(scan nfc.ScannedTag) {
		mu.Lock()
		seen[scan.Device]++
		mu.Unlock()
	})
	defer conn.Disconnect()

	var wg sync.WaitGroup
	for _, backend := range []*scanningManager{a, b, c} {
		wg.Add(1)
		go func(m *scanningManager) {
			defer wg.Done()
			for i := 0; i < perBackend; i++ {
				m.scans.Emit(nfc.ScannedTag{Device: m.name, Tag: nfc.NewMockTag("04FADE")})
			}
		}(backend)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"a", "b", "c"} {
		if seen[name] != perBackend {
			t.Errorf("backend %s: aggregate saw %d scans, want %d", name, seen[name], perBackend)
		}
	}
}

// TestSim_DeviceChangeStormReachesEveryWatcher storms the aggregate with device
// changes from several backends while several watchers read, then closes. Every
// watcher must observe a change and every watch channel must close, with no
// deadlock and no data race (run under -race) — this is the fan-out the agent's
// watcher and the supervisor's both depend on.
func TestSim_DeviceChangeStormReachesEveryWatcher(t *testing.T) {
	children := []*notifyingManager{
		newNotifyingManager("hw-1"),
		newNotifyingManager("hw-2"),
		newNotifyingManager("hw-3"),
	}
	entries := make([]ManagerEntry, len(children))
	for i, ch := range children {
		entries[i] = ManagerEntry{Name: ch.name, Manager: ch}
	}
	mm := NewMultiManager(entries...)

	const watchers = 4
	var wg sync.WaitGroup
	saw := make([]int, watchers)
	closed := make([]bool, watchers)
	for w := 0; w < watchers; w++ {
		ch := mm.DeviceChanges()
		wg.Add(1)
		go func(idx int, c <-chan struct{}) {
			defer wg.Done()
			for range c {
				saw[idx]++
			}
			closed[idx] = true
		}(w, ch)
	}

	// Storm: every backend reports changes as fast as it can for a moment.
	var storm sync.WaitGroup
	stop := make(chan struct{})
	for _, ch := range children {
		storm.Add(1)
		go func(c *notifyingManager) {
			defer storm.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c.notify()
				}
			}
		}(ch)
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	storm.Wait()

	// Give the fan-out a moment to deliver the last changes before closing.
	time.Sleep(50 * time.Millisecond)
	mm.Close()
	wg.Wait()

	for w := 0; w < watchers; w++ {
		if saw[w] == 0 {
			t.Errorf("watcher %d saw no change during the storm", w)
		}
		if !closed[w] {
			t.Errorf("watcher %d channel was not closed by Close", w)
		}
	}
}

// flappingManager lists devices only every other call, standing in for a backend
// whose connection drops and returns.
type flappingManager struct {
	mockManager
	mu    sync.Mutex
	calls int
}

func (m *flappingManager) Devices() ([]nfc.DeviceListing, error) {
	m.mu.Lock()
	m.calls++
	up := m.calls%2 == 1
	m.mu.Unlock()
	if !up {
		return nil, fmt.Errorf("%s backend temporarily unavailable", m.name)
	}
	return listings(m.devices, nfc.DeviceCapabilities{CanPoll: true}), nil
}

// TestSim_FlappingBackendDoesNotHideStableOnes: one backend's listing drops in
// and out; the stable backend's devices must remain visible on every aggregate
// listing regardless. One failing lane must never take the others down with it.
func TestSim_FlappingBackendDoesNotHideStableOnes(t *testing.T) {
	stable := &mockManager{name: "stable", devices: []string{"reader-A"}}
	flapping := &flappingManager{mockManager: mockManager{name: "flappy", devices: []string{"reader-B"}}}
	mm := NewMultiManager(
		ManagerEntry{Name: "stable", Manager: stable},
		ManagerEntry{Name: "flappy", Manager: flapping},
	)
	defer mm.Close()

	for i := 0; i < 6; i++ {
		listings, err := mm.Devices()
		if err != nil {
			t.Fatalf("aggregate Devices() should not error when one backend is down: %v", err)
		}
		if !containsPath(listings, "stable:reader-A") {
			t.Errorf("iteration %d: the stable backend's reader vanished from the listing", i)
		}
	}
}

func containsPath(listings []nfc.DeviceListing, path string) bool {
	for _, l := range listings {
		if l.Path == path {
			return true
		}
	}
	return false
}
