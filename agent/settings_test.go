package agent

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// newReaderAgent is an agent with a reader attached, without the servers Start
// would also bring up.
func newReaderAgent(t *testing.T) *Agent {
	t.Helper()

	agent := New(nfc.NewMockManager())
	reader, err := nfc.NewNFCReader("mock:usb:001", agent.Manager, time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	t.Cleanup(reader.Stop)

	agent.Reader = reader
	return agent
}

// What the agent is asked to apply is what it reports it is set to. The console
// renders from this answer, so a field that does not survive the round trip is
// a preference the operator sets and never sees again.
func TestSettingsRoundTripThroughTheAgent(t *testing.T) {
	agent := newReaderAgent(t)
	cardType := nfc.GetAllCardTypes()[0]

	want := settings.Settings{
		Mode:                settings.ModeReadOnly,
		CardTypes:           []string{cardType},
		DevicePath:          "ACS ACR1252U 01 00",
		Port:                9480,
		RequirePairedDevice: true,
		ReaderFeedback:      true,
	}
	agent.ApplySettings(want)

	if got := agent.Settings(); !reflect.DeepEqual(got, want) {
		t.Errorf("Settings() = %+v, want %+v", got, want)
	}
}

// The reader is built in Start, after the stored settings have been applied. A
// mode that only ever reached the reader would be dropped by every restart.
func TestStoredModeReachesTheReaderStarted(t *testing.T) {
	agent := New(nfc.NewMockManager())
	agent.ApplySettings(settings.Settings{Mode: settings.ModeReadOnly, ReaderFeedback: true})

	if agent.Settings().Mode != settings.ModeReadOnly {
		t.Fatalf("the agent did not hold the stored mode with no reader running")
	}

	reader, err := nfc.NewNFCReader("mock:usb:001", agent.Manager, time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	defer reader.Stop()

	agent.Reader = reader
	agent.adoptReaderSettings()

	if got := reader.GetMode(); got != nfc.ModeReadOnly {
		t.Errorf("reader mode = %v, want ModeReadOnly", got)
	}
	if !reader.FeedbackEnabled() {
		t.Error("the reader was started without the stored feedback preference")
	}
}

// A mode switched from the tray reaches the console, because both read it from
// the agent rather than from the file the tray never wrote to.
func TestSettingsReportTheLiveReaderMode(t *testing.T) {
	agent := newReaderAgent(t)
	agent.ApplySettings(settings.Settings{Mode: settings.ModeReadWrite})

	agent.SetReaderMode(nfc.ModeWriteOnly)

	if got := agent.Reader.GetMode(); got != nfc.ModeWriteOnly {
		t.Errorf("reader mode = %v, want ModeWriteOnly", got)
	}
	if got := agent.Settings().Mode; got != settings.ModeWriteOnly {
		t.Errorf("Settings().Mode = %q, want %q", got, settings.ModeWriteOnly)
	}
}

// An empty filter is not the same as one naming every type. A phone reports tag
// types this agent does not enumerate, and those have to be admitted too.
func TestCardTypeFilterIsSortedAndNilWhenOpen(t *testing.T) {
	agent := New(nfc.NewMockManager())

	if got := agent.Settings().CardTypes; got != nil {
		t.Errorf("CardTypes = %v, want nil for an unfiltered agent", got)
	}

	types := nfc.GetAllCardTypes()
	if len(types) < 2 {
		t.Skip("need two card types to check the ordering")
	}
	agent.ApplySettings(settings.Settings{CardTypes: []string{types[1], types[0]}})

	want := []string{types[0], types[1]}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if got := agent.Settings().CardTypes; !reflect.DeepEqual(got, want) {
		t.Errorf("CardTypes = %v, want %v", got, want)
	}
}

// The port is the settings' to set, unless the launcher placed the agent on
// one. A preference file may not move a listener an operator put somewhere.
func TestStoredPortMovesTheAgentUnlessTheLauncherSetIt(t *testing.T) {
	agent := New(nfc.NewMockManager())
	agent.ApplySettings(settings.Settings{Port: 9480})

	if got := agent.ConfiguredPort(); got != 9480 {
		t.Errorf("ConfiguredPort() = %d, want 9480", got)
	}

	held := New(nfc.NewMockManager())
	held.DevicePort = 9470
	held.SetExplicit(settings.Explicit{Port: true})
	held.ApplySettings(settings.Settings{Port: 9480})

	if got := held.ConfiguredPort(); got != 9470 {
		t.Errorf("a stored port moved a listener the launcher placed: port = %d", got)
	}
}

// What the launcher set, the run keeps: not from the file, and not from an
// operator either. A change accepted and then quietly dropped is what leaves
// someone believing the reader is read-only while it writes.
func TestTheLauncherHoldsEveryFieldItSet(t *testing.T) {
	cardType := nfc.GetAllCardTypes()[0]

	launched := settings.Settings{
		Mode:                settings.ModeReadOnly,
		CardTypes:           []string{cardType},
		DevicePath:          "ACS ACR1252U 01 00",
		Port:                9470,
		RequirePairedDevice: true,
		ReaderFeedback:      true,
	}

	agent := newReaderAgent(t)
	agent.ApplySettings(launched)
	agent.SetExplicit(settings.Explicit{
		Mode: true, CardTypes: true, DevicePath: true,
		Port: true, RequirePairedDevice: true, ReaderFeedback: true,
	})

	// A stored file asking for the opposite of all of it.
	agent.ApplySettings(settings.Settings{
		Mode:                settings.ModeWriteOnly,
		CardTypes:           nil,
		DevicePath:          "",
		Port:                9480,
		RequirePairedDevice: false,
		ReaderFeedback:      false,
	})
	if got := agent.Settings(); !reflect.DeepEqual(got, launched) {
		t.Errorf("a stored file moved what the launcher set:\n got %+v\nwant %+v", got, launched)
	}

	// And an operator at a menu, which reaches the setters directly.
	agent.SetReaderMode(nfc.ModeWriteOnly)
	agent.SetCardTypeFilter(nil)
	agent.SetPinnedDevice("")
	agent.SetRequirePairedDevice(false)
	agent.SetReaderFeedback(false)
	if got := agent.Settings(); !reflect.DeepEqual(got, launched) {
		t.Errorf("a menu moved what the launcher set:\n got %+v\nwant %+v", got, launched)
	}
}

// Only the fields it actually set. Marking one must not freeze the rest.
func TestTheLauncherHoldsNothingElse(t *testing.T) {
	agent := newReaderAgent(t)
	agent.SetExplicit(settings.Explicit{Port: true})
	agent.ApplySettings(settings.Settings{Mode: settings.ModeReadOnly, ReaderFeedback: true})

	if got := agent.Settings().Mode; got != settings.ModeReadOnly {
		t.Errorf("Mode = %q, want %q", got, settings.ModeReadOnly)
	}
	if !agent.Settings().ReaderFeedback {
		t.Error("a held port also froze reader feedback")
	}
}

// A settings file with nothing in it means the defaults, not zero values. An
// unset mode must not leave the reader in one it has no name for.
func TestEmptySettingsAreTheDefaults(t *testing.T) {
	agent := New(nfc.NewMockManager())
	agent.ApplySettings(settings.Settings{})

	if got := agent.Settings().Mode; got != settings.ModeReadWrite {
		t.Errorf("Mode = %q, want %q", got, settings.ModeReadWrite)
	}
}

// The console draws a snapshot from its own goroutines while the tray changes
// what it is drawing. The card-type filter is a map, so a write during a read
// of one takes the process down rather than merely racing.
func TestSettingsAreSafeToReadWhileTheyChange(t *testing.T) {
	agent := New(nfc.NewMockManager())
	types := nfc.GetAllCardTypes()
	modes := []nfc.ReaderMode{nfc.ModeReadWrite, nfc.ModeReadOnly, nfc.ModeWriteOnly}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = agent.Settings()
				}
			}
		}()
	}

	for i := range 300 {
		agent.SetReaderMode(modes[i%len(modes)])
		agent.SetAllowCardType(types[i%len(types)], i%2 == 0)
		agent.SetRequirePairedDevice(i%2 == 0)
		agent.SetReaderFeedback(i%3 == 0)
		agent.ApplySettings(settings.Settings{Mode: settings.ModeReadWrite, Port: 9470 + i%3})
	}

	close(stop)
	readers.Wait()
}
