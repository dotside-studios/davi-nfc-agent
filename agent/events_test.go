package agent

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func eventsAgent(t *testing.T) *Agent {
	t.Helper()

	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return rt.Agent
}

// A console connects to Any once instead of to each signal, so a change that
// does not reach it is a page that stops redrawing.
func TestAnyNamesEveryChange(t *testing.T) {
	a := eventsAgent(t)

	var seen []Change
	a.Events().Any.Connect(func(c Change) { seen = append(seen, c) })

	a.fireState(StateRunning)
	a.SetReaderMode(nfc.ModeReadOnly)
	a.fireServerRestart()

	want := []Change{
		ChangeState,
		ChangePreferences,
		ChangeServers,
	}
	if len(seen) != len(want) {
		t.Fatalf("Any carried %v, want %v", seen, want)
	}
	for i, c := range want {
		if seen[i] != c {
			t.Errorf("Any carried %v at %d, want %v", seen[i], i, c)
		}
	}
}

// Scans are traffic rather than a change of state, so a subscriber redrawing on
// Any does not redraw per card.
func TestScansDoNotReachAny(t *testing.T) {
	a := eventsAgent(t)

	var any int
	var tags int
	a.Events().Any.Connect(func(Change) { any++ })
	a.Events().Tag.Connect(func(nfc.NFCData) { tags++ })

	a.events.Tag.Emit(nfc.NFCData{})

	if tags != 1 {
		t.Errorf("Tag fired %d times, want 1", tags)
	}
	if any != 0 {
		t.Errorf("Any fired %d times for a scan, want 0", any)
	}
}

// The typed signal comes first, so a subscriber to both has the new value in
// hand by the time Any tells it something moved.
func TestTheTypedSignalFiresBeforeAny(t *testing.T) {
	a := eventsAgent(t)

	var order []string
	a.Events().Preferences.Connect(func(Preferences) { order = append(order, "typed") })
	a.Events().Any.Connect(func(Change) { order = append(order, "any") })

	// Preferences is a Property, so connecting replayed the current value.
	order = nil

	a.SetReaderMode(nfc.ModeReadOnly)

	if len(order) != 2 || order[0] != "typed" || order[1] != "any" {
		t.Errorf("signals fired %v, want [typed any]", order)
	}
}

func TestPreferencesCarriesTheNewValue(t *testing.T) {
	a := eventsAgent(t)

	var got Preferences
	a.Events().Preferences.Connect(func(p Preferences) { got = p })

	a.SetReaderMode(nfc.ModeReadOnly)

	if got.Mode != nfc.ModeReadOnly {
		t.Errorf("subscriber saw mode %v, want %v", got.Mode, nfc.ModeReadOnly)
	}
}

// A restart is where the address a menu or a page shows can have changed, so
// the port is what the signal carries.
func TestServersCarriesThePort(t *testing.T) {
	a := eventsAgent(t)

	got := -1
	a.Events().Servers.Connect(func(port int) { got = port })

	a.fireServerRestart()

	if got != a.DevicePort() {
		t.Errorf("subscriber saw port %d, want %d", got, a.DevicePort())
	}
}

func TestChangeString(t *testing.T) {
	if got := ChangePreferences.String(); got != "preferences" {
		t.Errorf("ChangePreferences is %q, want %q", got, "preferences")
	}
	if got := Change(42).String(); got != "change(42)" {
		t.Errorf("an unknown change is %q, want %q", got, "change(42)")
	}
}

// fakeManager is a manager whose reader set the test drives.
type fakeManager struct {
	devices []string
	readers []string
	changes chan struct{}
}

func (m *fakeManager) OpenDevice(string) (nfc.Device, error) {
	return nil, errors.New("fakeManager opens nothing")
}

// Devices marks the ones in readers as pollable, the rest as reporting for
// themselves.
func (m *fakeManager) Devices() ([]nfc.DeviceListing, error) {
	out := make([]nfc.DeviceListing, 0, len(m.devices))
	for _, path := range m.devices {
		caps := nfc.DeviceCapabilities{SupportsEvents: true}
		if slices.Contains(m.readers, path) {
			caps = nfc.DeviceCapabilities{CanPoll: true}
		}
		out = append(out, nfc.DeviceListing{Path: path, Capabilities: caps})
	}
	return out, nil
}
func (m *fakeManager) DeviceChanges() <-chan struct{} { return m.changes }

// A phone is a tag source, not a reader. Offering one in a reader picker pins
// the reader to a device that is never opened.
func TestReadersLeaveOutPhones(t *testing.T) {
	m := &fakeManager{
		devices: []string{"ACR122U 00 00", "phone-9f2a"},
		readers: []string{"ACR122U 00 00"},
	}
	a := New(Config{Manager: m})

	got := a.Readers()
	if len(got) != 1 || got[0] != "ACR122U 00 00" {
		t.Errorf("Readers() = %v, want only the hardware reader", got)
	}
}

// A reader plugged in or unplugged reaches a subscriber, so a picker redraws
// without polling the manager behind the agent's back.
func TestReaderChangesReachSubscribers(t *testing.T) {
	m := &fakeManager{
		devices: []string{"ACR122U 00 00"},
		readers: []string{"ACR122U 00 00"},
		changes: make(chan struct{}, 1),
	}
	a := New(Config{Manager: m})
	defer a.Shutdown()

	seen := make(chan []string, 1)
	a.Events().Readers.Connect(func(readers []string) {
		select {
		case seen <- readers:
		default:
		}
	})

	m.changes <- struct{}{}

	select {
	case got := <-seen:
		if len(got) != 1 || got[0] != "ACR122U 00 00" {
			t.Errorf("subscriber saw %v, want the reader list", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a device change never reached a subscriber")
	}
}

// A subscriber draws its first frame from the signal it follows. Reading the
// agent separately leaves a gap where a change between the read and the connect
// is missed, and made every consumer repeat the same initial pull.
func TestTheStateSignalsReportTheCurrentValue(t *testing.T) {
	a := eventsAgent(t)
	a.SetReaderMode(nfc.ModeReadOnly)

	events := a.Events()

	var state State
	events.State.Connect(func(s State) { state = s })
	if state != a.State() {
		t.Errorf("State replayed %v, want %v", state, a.State())
	}

	var prefs Preferences
	events.Preferences.Connect(func(p Preferences) { prefs = p })
	if prefs.Mode != nfc.ModeReadOnly {
		t.Errorf("Preferences replayed mode %v, want %v", prefs.Mode, nfc.ModeReadOnly)
	}

	port := -1
	events.Servers.Connect(func(p int) { port = p })
	if port != a.DevicePort() {
		t.Errorf("Servers replayed %d, want %d", port, a.DevicePort())
	}

	var readers []string
	events.Readers.Connect(func(r []string) { readers = r })
	if !slices.Equal(readers, a.Readers()) {
		t.Errorf("Readers replayed %v, want %v", readers, a.Readers())
	}

}

// Scans and reader status are traffic: there is no current one to replay, and a
// subscriber connecting must not be handed the last card as though it had just
// been presented.
func TestTrafficSignalsDoNotReplay(t *testing.T) {
	a := eventsAgent(t)
	a.reportTag(nfc.NFCData{Card: &nfc.Card{UID: "04A2"}})

	tags := 0
	a.Events().Tag.Connect(func(nfc.NFCData) { tags++ })
	if tags != 0 {
		t.Errorf("Tag replayed %d scans, want 0", tags)
	}

	status := 0
	a.Events().Reader.Connect(func(nfc.DeviceStatus) { status++ })
	if status != 0 {
		t.Errorf("Reader replayed %d times, want 0", status)
	}
}
