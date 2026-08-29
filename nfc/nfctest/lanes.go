package nfctest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Multi-lane emulation.
//
// A real deployment is rarely one reader. A checkout floor, a locker wall, an
// access gate with an entry and an exit lane — the agent drives several readers
// at once, each with its own card in the field, and an operation names the lane
// it is for. EmulatedLanes stands up that arrangement over a single production
// nfc.Supervisor so a test can present and remove cards per lane, write to a
// named lane, and plug and unplug lanes while the others keep working:
//
//	lanes := nfctest.NewEmulatedLanes(t, "entry", "exit")
//	lanes.Present("entry", nfctest.NTAG215("04AA").WithText("member"))
//	res, err := lanes.Write("entry", msg, nfc.WriteOptions{Overwrite: true, Index: -1})
//
// The single-reader NewEmulatedReader is the right tool for tag-driver and
// write-path behaviour; EmulatedLanes is for the orchestration across readers —
// routing, isolation between lanes, and hot-plug reconciliation.

// laneManager is a Manager offering one independent pollable device per lane,
// keyed by lane name. Unlike nfc.MockManager, which hands the same device to
// every path, each lane here has its own device and its own tags, so the
// supervisor opens a genuinely separate reader per lane. It notifies device
// changes so the supervisor reconciles when a lane is plugged or unplugged.
type laneManager struct {
	mu      sync.Mutex
	devices map[string]*nfc.MockDevice
	changes chan struct{}
}

func newLaneManager() *laneManager {
	return &laneManager{
		devices: make(map[string]*nfc.MockDevice),
		changes: make(chan struct{}, 1),
	}
}

// OpenDevice returns the device for the named lane. The supervisor connects each
// reader by the lane path it listed, so this must honour the path rather than
// return a single shared device.
func (m *laneManager) OpenDevice(path string) (nfc.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, ok := m.devices[path]
	if !ok {
		return nil, fmt.Errorf("nfctest: no lane %q", path)
	}
	dev.IsOpen = true
	return dev, nil
}

// Devices lists every plugged lane as a pollable reader, in a stable order.
func (m *laneManager) Devices() ([]nfc.DeviceListing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	paths := make([]string, 0, len(m.devices))
	for path := range m.devices {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	out := make([]nfc.DeviceListing, 0, len(paths))
	for _, path := range paths {
		out = append(out, nfc.DeviceListing{
			Path:         path,
			ID:           path,
			Capabilities: nfc.DeviceCapabilities{CanPoll: true, CanTransceive: true, DeviceType: "mock"},
		})
	}
	return out, nil
}

// DeviceChanges lets the supervisor learn when a lane is plugged or unplugged.
func (m *laneManager) DeviceChanges() <-chan struct{} { return m.changes }

func (m *laneManager) notify() {
	select {
	case m.changes <- struct{}{}:
	default:
	}
}

func (m *laneManager) add(path string, dev *nfc.MockDevice) {
	m.mu.Lock()
	m.devices[path] = dev
	m.mu.Unlock()
	m.notify()
}

func (m *laneManager) remove(path string) {
	m.mu.Lock()
	delete(m.devices, path)
	m.mu.Unlock()
	m.notify()
}

// lane is one reader's field: the device the supervisor polls and the cards
// currently on it.
type lane struct {
	dev   *nfc.MockDevice
	cards []*EmulatedCard
}

// EmulatedLanes drives several emulated readers through one nfc.Supervisor.
// Its embedded *nfc.Supervisor exposes the production per-lane operations
// (WriteMessage, Capabilities, Lock, Transceive), each taking a lane name.
type EmulatedLanes struct {
	*nfc.Supervisor
	tb    TB
	mgr   *laneManager
	mu    sync.Mutex
	lanes map[string]*lane
}

// NewEmulatedLanes builds a supervisor operating one reader per named lane, with
// no cards in any field yet. It waits for the supervisor to open every lane
// before returning, and stops it when the test ends.
func NewEmulatedLanes(tb TB, names ...string) *EmulatedLanes {
	tb.Helper()

	mgr := newLaneManager()
	l := &EmulatedLanes{tb: tb, mgr: mgr, lanes: make(map[string]*lane)}
	for _, name := range names {
		dev := nfc.NewMockDevice()
		dev.DeviceConnection = name
		mgr.devices[name] = dev
		l.lanes[name] = &lane{dev: dev}
	}

	sup, err := nfc.NewSupervisor(mgr, 5*time.Second)
	if err != nil {
		tb.Fatalf("nfctest: create lanes: %v", err)
	}
	if err := sup.Start(); err != nil {
		tb.Fatalf("nfctest: start lanes: %v", err)
	}
	l.Supervisor = sup
	tb.Cleanup(sup.Stop)

	l.AwaitLanes(len(names))
	return l
}

// Plug adds a new lane at runtime, as if a reader were connected, and waits for
// the supervisor to open it.
func (l *EmulatedLanes) Plug(name string) {
	l.tb.Helper()
	l.mu.Lock()
	if _, exists := l.lanes[name]; exists {
		l.mu.Unlock()
		return
	}
	dev := nfc.NewMockDevice()
	dev.DeviceConnection = name
	l.lanes[name] = &lane{dev: dev}
	want := len(l.lanes)
	l.mu.Unlock()

	l.mgr.add(name, dev)
	l.AwaitLanes(want)
}

// Unplug removes a lane at runtime, as if a reader were disconnected, and waits
// for the supervisor to drop it.
func (l *EmulatedLanes) Unplug(name string) {
	l.tb.Helper()
	l.mu.Lock()
	delete(l.lanes, name)
	want := len(l.lanes)
	l.mu.Unlock()

	l.mgr.remove(name)
	l.AwaitLanes(want)
}

// Present taps cards onto a lane.
func (l *EmulatedLanes) Present(laneName string, cards ...*EmulatedCard) {
	l.mu.Lock()
	ln := l.lanes[laneName]
	if ln != nil {
		ln.cards = append(ln.cards, cards...)
	}
	l.mu.Unlock()
	l.sync(laneName)
}

// Remove takes the card with the given UID off a lane.
func (l *EmulatedLanes) Remove(laneName, uid string) {
	l.mu.Lock()
	if ln := l.lanes[laneName]; ln != nil {
		kept := ln.cards[:0]
		for _, c := range ln.cards {
			if c.uid != uid {
				kept = append(kept, c)
			}
		}
		ln.cards = kept
	}
	l.mu.Unlock()
	l.sync(laneName)
}

// Write encodes a message onto the card on the named lane.
func (l *EmulatedLanes) Write(laneName string, msg *nfc.NDEFMessage, opts nfc.WriteOptions) (*nfc.WriteResult, error) {
	return l.WriteMessage(context.Background(), laneName, msg, opts)
}

// Capabilities reports what the card on the named lane supports.
func (l *EmulatedLanes) Capabilities(laneName string) (*nfc.TagCapabilities, error) {
	return l.Supervisor.Capabilities(context.Background(), laneName, "")
}

// sync pushes a lane's current card set to its device.
func (l *EmulatedLanes) sync(laneName string) {
	l.mu.Lock()
	ln := l.lanes[laneName]
	if ln == nil {
		l.mu.Unlock()
		return
	}
	tags := make([]nfc.Tag, len(ln.cards))
	for i, c := range ln.cards {
		tags[i] = c.tag
	}
	dev := ln.dev
	l.mu.Unlock()
	dev.SetTags(tags)
}

// AwaitLanes waits until the supervisor is operating exactly n readers, so a
// test does not race the reconcile that a plug or unplug kicks off. It fails the
// test if the count is not reached in time.
func (l *EmulatedLanes) AwaitLanes(n int) {
	l.tb.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.Devices()) == n {
			// Let the newly opened readers establish their initial connection.
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.tb.Fatalf("nfctest: supervisor operating %d lanes, want %d", len(l.Devices()), n)
}
