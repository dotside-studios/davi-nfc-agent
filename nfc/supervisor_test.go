package nfc

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func startedSupervisor(t *testing.T, m Manager) *Supervisor {
	t.Helper()

	s, err := NewSupervisor(m, time.Second)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// Every reader the manager offers is operated, not one chosen at startup.
func TestASupervisorOperatesEveryReader(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}

	s := startedSupervisor(t, m)

	devices := s.Devices()
	if len(devices) != 2 || devices[0] != "mock:usb:001" || devices[1] != "mock:usb:002" {
		t.Errorf("Devices() = %v, want both readers", devices)
	}
}

// A scan names the reader it was read on, which is what tells two of them
// apart.
func TestASupervisorScanNamesItsReader(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:007"}
	m.MockDevice.SetTags([]Tag{NewMockTag("04A1B2C3")})

	s := startedSupervisor(t, m)

	seen := make(chan NFCData, 8)
	s.Scans().Connect(func(data NFCData) {
		select {
		case seen <- data:
		default:
		}
	})

	select {
	case data := <-seen:
		if data.Device != "mock:usb:007" {
			t.Errorf("the scan names %q, want the reader it was read on", data.Device)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no scan arrived from the reader")
	}
}

// An operation names the reader it applies to. Unnamed is the only reader there
// is, and is refused once there is more than one rather than picking for the
// caller.
func TestAnOperationNamesItsReader(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}

	s := startedSupervisor(t, m)

	if _, _, err := s.readerFor(""); err == nil {
		t.Error("an unnamed operation was served with two readers connected")
	} else if !strings.Contains(err.Error(), "mock:usb:001") {
		t.Errorf("the refusal is %q, want it to name the readers", err)
	}

	if _, _, err := s.readerFor("mock:usb:002"); err != nil {
		t.Errorf("naming a connected reader failed: %v", err)
	}
	if _, _, err := s.readerFor("nothing-here"); err == nil {
		t.Error("an operation for a reader that is not connected was accepted")
	}
}

// One reader is the ordinary case, and naming it is optional there.
func TestOneReaderNeedsNoName(t *testing.T) {
	s := startedSupervisor(t, NewMockManager())

	name, _, err := s.readerFor("")
	if err != nil {
		t.Fatalf("readerFor(\"\") with one reader: %v", err)
	}
	if name != "mock:usb:001" {
		t.Errorf("resolved to %q, want the only reader", name)
	}
}

// Policy belongs to the supervisor, so a reader that joins later runs under
// what was already set rather than under its own defaults.
func TestPolicyReachesAReaderOpenedLater(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001"}

	s := startedSupervisor(t, m)
	s.SetMode(ModeReadOnly)
	s.SetFeedback(true)

	m.DevicesList = append(m.DevicesList, "mock:usb:002")
	s.reconcile()

	_, joined, err := s.readerFor("mock:usb:002")
	if err != nil {
		t.Fatalf("the second reader was not opened: %v", err)
	}
	if joined.GetMode() != ModeReadOnly {
		t.Errorf("the reader that joined is in mode %v, want %v", joined.GetMode(), ModeReadOnly)
	}
	if !joined.FeedbackEnabled() {
		t.Error("the reader that joined has feedback off, want it on")
	}
}

// A reader the manager stops offering is dropped, so the supervisor stops
// claiming to operate something that is gone.
func TestAnUnpluggedReaderIsDropped(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}

	s := startedSupervisor(t, m)

	m.DevicesList = []string{"mock:usb:001"}
	s.reconcile()

	if devices := s.Devices(); len(devices) != 1 || devices[0] != "mock:usb:001" {
		t.Errorf("Devices() = %v, want only the reader still connected", devices)
	}
}

// Starting twice is a mistake in the program, not something to do quietly, and
// a stopped supervisor stays stopped: its readers are closed and the goroutines
// behind them have ended.
func TestASupervisorStartsOnce(t *testing.T) {
	s := startedSupervisor(t, NewMockManager())

	if err := s.Start(); err == nil {
		t.Error("a second Start was accepted")
	}

	s.Stop()
	if err := s.Start(); err == nil {
		t.Error("a stopped supervisor was started again")
	}
}

// What the router asks is answered from what a reader last scanned: the answer
// picks the reader, which checks the tag it has when the operation runs. A
// reader that has scanned nothing has no tag to route to.
func TestASupervisorAnswersForTheTagOnAReader(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001"}
	m.MockDevice.SetTags(nil)

	s := startedSupervisor(t, m)

	if device, uid, ok := s.TagOn("mock:usb:001"); ok {
		t.Errorf("TagOn = %q, %q, %v; want no tag on a reader that has scanned none", device, uid, ok)
	}
	if got := s.DevicesHoldingTags(); len(got) != 0 {
		t.Errorf("DevicesHoldingTags = %v, want none", got)
	}

	tag := NewMockTag("04A1B2C3")
	if err := tag.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	m.MockDevice.SetTags([]Tag{tag})

	scanned := make(chan struct{}, 1)
	conn := s.Scans().Connect(func(NFCData) {
		select {
		case scanned <- struct{}{}:
		default:
		}
	})
	defer conn.Disconnect()

	select {
	case <-scanned:
	case <-time.After(3 * time.Second):
		t.Fatal("no scan arrived from the reader")
	}

	device, uid, ok := s.TagOn("mock:usb:001")
	if !ok || device != "mock:usb:001" || uid != "04A1B2C3" {
		t.Errorf("TagOn = %q, %q, %v; want the reader and the tag it scanned", device, uid, ok)
	}
	if got := s.DevicesHoldingTags(); len(got) != 1 || got[0] != "mock:usb:001" {
		t.Errorf("DevicesHoldingTags = %v, want [mock:usb:001]", got)
	}
	if _, _, ok := s.TagOn("nothing-here"); ok {
		t.Error("a reader that is not connected was reported as holding a tag")
	}
}

// The router names the tag an operation is for, and the supervisor hands that
// name to the reader rather than dropping it: the reader has the tag, and is
// the only thing that can refuse one that was swapped since the request was
// made.
func TestASupervisorHoldsAnOperationToTheTagItNamed(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001"}
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true
	m.MockDevice.SetTags([]Tag{tag})

	s := startedSupervisor(t, m)

	if _, err := s.WriteTag("mock:usb:001", "04FFFFFF", textMessage("for another tag"), false, ""); !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("WriteTag err = %v, want ErrTagUIDMismatch", err)
	}
	if _, err := s.LockTag("mock:usb:001", "04FFFFFF", ""); !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("LockTag err = %v, want ErrTagUIDMismatch", err)
	}
	if tag.IsReadOnly {
		t.Error("a tag the caller did not name was locked, which cannot be undone")
	}
	if _, err := s.TransceiveTag("mock:usb:001", "04FFFFFF", []byte{0x30, 0x00}, true); !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("TransceiveTag err = %v, want ErrTagUIDMismatch", err)
	}
	if _, err := s.TagCapabilities("mock:usb:001", "04FFFFFF"); !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("TagCapabilities err = %v, want ErrTagUIDMismatch", err)
	}
}

// The supervisor answers for the tags the manager's own devices hold as well as
// the ones on its readers. A phone's scan already arrives on the supervisor's
// signal, so what can be done to it is asked in the same place rather than
// through a second route the caller has to know about.
func TestASupervisorAnswersForTheManagersDevices(t *testing.T) {
	m := &holdingManager{Manager: NewMockManager(), held: map[string]string{"phone-9f2a": "04DEADBEEF"}}
	s := startedSupervisor(t, m)

	device, uid, ok := s.TagOn("phone-9f2a")
	if !ok || device != "phone-9f2a" || uid != "04DEADBEEF" {
		t.Errorf("TagOn = %q, %q, %v; want the phone and the tag it holds", device, uid, ok)
	}
	if got := s.DevicesHoldingTags(); len(got) != 1 || got[0] != "phone-9f2a" {
		t.Errorf("DevicesHoldingTags = %v, want the phone", got)
	}
	if s.Operates("phone-9f2a") {
		t.Error("a device that reports its own scans was reported as a reader the supervisor opened")
	}

	if _, err := s.WriteTag("phone-9f2a", "04DEADBEEF", NewNDEFMessage(), false, "key-1"); err != nil {
		t.Fatalf("WriteTag: %v", err)
	}
	if !m.wrote["phone-9f2a"] {
		t.Error("the write did not reach the device holding the tag")
	}
}

// A reader holding a tag it could not read still has a tag to operate on: a
// blank or damaged one never reaches the last scan, and refusing it would
// refuse exactly the tag a client asks to write.
func TestASupervisorNamesAReaderHoldingATagItCouldNotRead(t *testing.T) {
	m := NewMockManager()
	m.MockDevice.SetTags([]Tag{NewMockTag("04A1B2C3")}) // never connected, so reads fail

	s := startedSupervisor(t, m)

	awaitHeld(t, s, "mock:usb:001")

	if device, uid, ok := s.TagOn(""); !ok || device != "mock:usb:001" || uid != "" {
		t.Errorf("TagOn = %q, %q, %v; want the reader holding a tag it cannot name", device, uid, ok)
	}
}

// holdingManager is a manager whose devices hold tags of their own, which is
// what the phone driver is.
type holdingManager struct {
	Manager
	held  map[string]string // device -> tag UID
	wrote map[string]bool

	// opensReaders lets a test give the supervisor a reader to poll as well,
	// which is what a build with a phone driver beside a reader has.
	opensReaders bool
}

func (m *holdingManager) RemoteDevices() bool { return !m.opensReaders }

func (m *holdingManager) TagOn(device string) (string, string, bool) {
	if device == "" {
		for id, uid := range m.held {
			return id, uid, true
		}
		return "", "", false
	}
	uid, ok := m.held[device]
	return device, uid, ok
}

func (m *holdingManager) DevicesHoldingTags() []string {
	out := make([]string, 0, len(m.held))
	for id := range m.held {
		out = append(out, id)
	}
	return out
}

func (m *holdingManager) WriteTag(device, uid string, _ *NDEFMessage, lock bool, _ string) (*WriteResult, error) {
	if _, ok := m.held[device]; !ok {
		return nil, errors.New("device is not holding a tag")
	}
	if m.wrote == nil {
		m.wrote = map[string]bool{}
	}
	m.wrote[device] = true
	return &WriteResult{UID: uid, Locked: lock}, nil
}

func (m *holdingManager) LockTag(device, uid, _ string) (*LockResult, error) {
	return &LockResult{UID: uid, Locked: true}, nil
}

func (m *holdingManager) TransceiveTag(_, _ string, data []byte, _ bool) ([]byte, error) {
	return data, nil
}

func (m *holdingManager) TagCapabilities(device, _ string) (*TagCapabilities, error) {
	uid, ok := m.held[device]
	if !ok {
		return nil, errors.New("device is not holding a tag")
	}
	caps := GetTagCapabilities(NewMockTag(uid))
	return &caps, nil
}

// An unnamed request prefers a tag that can be named over one a reader is
// holding but could not read: naming it is the only thing that tells a client
// which tag the operation reached.
func TestAnUnnamedRequestPrefersATagThatCanBeNamed(t *testing.T) {
	mock := NewMockManager()
	mock.MockDevice.SetTags([]Tag{NewMockTag("04A1B2C3")}) // never connected, so reads fail

	m := &holdingManager{
		Manager:      mock,
		held:         map[string]string{"phone-9f2a": "04DEADBEEF"},
		opensReaders: true,
	}
	s := startedSupervisor(t, m)

	awaitHeld(t, s, "mock:usb:001")

	device, uid, ok := s.TagOn("")
	if !ok || device != "phone-9f2a" || uid != "04DEADBEEF" {
		t.Errorf("TagOn = %q, %q, %v; want the tag the device could name", device, uid, ok)
	}
}

// awaitHeld waits for a device to report the tag on it, so a question does not
// overtake the poll it depends on.
func awaitHeld(t *testing.T, s *Supervisor, device string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := s.TagOn(device); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reported the tag on it", device)
}

// The supervisor opens what a manager offers as pollable and nothing else:
// connecting to a device that reports its own scans cannot succeed.
func TestASupervisorOpensOnlyWhatCanBePolled(t *testing.T) {
	m := &mixedManager{Manager: NewMockManager()}
	s := startedSupervisor(t, m)

	if devices := s.Devices(); len(devices) != 1 || devices[0] != "mock:usb:001" {
		t.Errorf("Devices() = %v, want the reader alone", devices)
	}
	if s.Operates("phone-9f2a") {
		t.Error("a device that reports its own scans was opened as a reader")
	}
}

// mixedManager offers a reader and a device that reports for itself, as a build
// with a phone driver beside a reader does.
type mixedManager struct {
	Manager
}

func (m *mixedManager) Devices() ([]DeviceListing, error) {
	return []DeviceListing{
		{Path: "mock:usb:001", Capabilities: DeviceCapabilities{CanPoll: true}},
		{Path: "phone-9f2a", Capabilities: DeviceCapabilities{SupportsEvents: true}},
	}, nil
}
