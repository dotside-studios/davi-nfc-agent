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
