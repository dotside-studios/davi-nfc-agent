package tray

import (
	"io"
	"log"
	"strings"
	"testing"

	nfcagent "github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func newTestAgent() *nfcagent.Agent {
	return newTestAgentWith(nfcagent.Config{})
}

// newTestAgentWith builds a test agent around cfg. The stores are settled at
// construction here rather than assigned afterwards, which is what Config is for.
func newTestAgentWith(cfg nfcagent.Config) *nfcagent.Agent {
	cfg.Manager = nfc.NewMockManager()
	cfg.Logger = log.New(io.Discard, "", 0)
	return nfcagent.New(cfg)
}

// newTestTray builds the real tray menu on a fake driver, so it can be read and
// clicked with no desktop involved.
func newTestTray(t *testing.T, a *nfcagent.Agent) (*App, *traymenu.Fake) {
	t.Helper()

	fake := traymenu.NewFake()
	app := newApp(&nfcagent.Runtime{Agent: a}, fake)
	t.Cleanup(app.menu.Close)
	app.setupUI()
	return app, fake
}

// titles lists the top level as the user reads it, separators included.
func titles(fake *traymenu.Fake) []string {
	var out []string
	for _, item := range fake.Items() {
		if item.IsSeparator() {
			out = append(out, "----")
			continue
		}
		out = append(out, item.Title())
	}
	return out
}

func TestMenuLayout(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	want := []string{
		"Starting...",
		"----",
		"Card UID: None",
		"Card Type: None",
		"----",
		"Device",
		"----",
		"Mode: Read/Write",
		"Flash and Beep on Scan",
		"----",
		"Card Type Filter",
		"----",
		"Paired Devices",
		"Allowed Origins",
		"----",
		"Start Agent",
		"Stop Agent",
		"----",
		"Quit",
	}

	got := titles(fake)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("menu reads:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The entry that opens the console belongs to the console plugin, so a tray
// with no plugin behind it declares none of its own.
func TestTheTrayDeclaresNoConsoleEntry(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	if item := fake.Find("Open Control Center"); item != nil {
		t.Errorf("the tray declares a control center entry with no plugin behind it:\n%s", fake.Render())
	}
}

func TestStatusAndCardLabelsAreNotClickable(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	for _, title := range []string{"Starting...", "Card UID: None", "Card Type: None"} {
		if item := fake.Find(title); item == nil || item.Enabled() {
			t.Errorf("%q should be a disabled label", title)
		}
	}
}

func TestAgentControlsStartDisabled(t *testing.T) {
	app, _ := newTestTray(t, newTestAgent())

	// The agent auto-starts, so neither control has anything to do until it has
	// come up or failed.
	if app.mStart.Enabled() || app.mStop.Enabled() {
		t.Fatalf("Start/Stop enabled = %v/%v, want false/false", app.mStart.Enabled(), app.mStop.Enabled())
	}
}

func TestCardTypeFilterTogglesOffAllTypes(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	cardType := nfc.GetAllCardTypes()[0]
	filter := app.cardTypes.Item(cardType)

	filter.Click()

	if !filter.Checked() {
		t.Error("the clicked card type is not ticked")
	}
	if app.cardTypes.All().Checked() {
		t.Error("All Types is still ticked after picking one type")
	}
	if !agent.IsCardTypeAllowed(cardType) {
		t.Errorf("%s was not allowed on the agent", cardType)
	}
}

func TestUntickingTheLastCardTypeRevertsToAllTypes(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	cardType := nfc.GetAllCardTypes()[0]
	filter := app.cardTypes.Item(cardType)

	filter.Click()
	filter.Click()

	if filter.Checked() {
		t.Error("the card type is still ticked after a second click")
	}
	if !app.cardTypes.All().Checked() {
		t.Error("All Types was not restored when the last filter came off")
	}
	if agent.AllowedCardTypesLength() != 0 {
		t.Errorf("agent still filters on %d card types", agent.AllowedCardTypesLength())
	}
}

func TestAllTypesClearsIndividualFilters(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	cardType := nfc.GetAllCardTypes()[0]
	app.cardTypes.Item(cardType).Click()

	app.cardTypes.All().Click()

	if app.cardTypes.Item(cardType).Checked() {
		t.Error("an individual filter is still ticked under All Types")
	}
	if !app.cardTypes.All().Checked() {
		t.Error("All Types is not ticked")
	}
	if agent.AllowedCardTypesLength() != 0 {
		t.Errorf("All Types left a filter of %d types on the agent", agent.AllowedCardTypesLength())
	}
	// A phone can report a tag type this agent does not enumerate, and All
	// Types has to mean that one too.
	if !agent.IsCardTypeAllowed("MIFARE Plus") {
		t.Error("All Types refuses a card type the agent does not know")
	}
}

// The mode is the agent's, not the running reader's, so it can be picked with
// the agent stopped and the reader Start builds is started in it. The tick used
// to spring back here, leaving the operator with no way to tell which mode the
// next reader would use.
func TestModeMenuHoldsWithoutAReader(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	app.modes.Item(nfc.ModeWriteOnly).Click()

	if got, _ := app.modes.Value(); got != nfc.ModeWriteOnly {
		t.Errorf("mode = %v, want ModeWriteOnly", got)
	}
	if app.mModeMenu.Title() != "Mode: Write Only" {
		t.Errorf("mode menu title = %q, want %q", app.mModeMenu.Title(), "Mode: Write Only")
	}
	// And the agent is what holds it, so the console shows the same thing.
	if got := agent.CurrentReaderMode(); got != nfc.ModeWriteOnly {
		t.Errorf("agent mode = %v, want ModeWriteOnly", got)
	}
	if got := agent.Preferences().Mode; got != nfc.ModeWriteOnly {
		t.Errorf("agent settings mode = %v, want %v", got, nfc.ModeWriteOnly)
	}
}

func TestSyncPreferencesToMenu(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	cardType := nfc.GetAllCardTypes()[0]
	app.SyncPreferencesToMenu(nfcagent.Preferences{
		Mode:           nfc.ModeWriteOnly,
		CardTypes:      []string{cardType},
		ReaderFeedback: true,
	})

	if got, _ := app.modes.Value(); got != nfc.ModeWriteOnly {
		t.Errorf("mode = %v, want ModeWriteOnly", got)
	}
	if app.mModeMenu.Title() != "Mode: Write Only" {
		t.Errorf("mode menu title = %q, want %q", app.mModeMenu.Title(), "Mode: Write Only")
	}
	if !app.cardTypes.Item(cardType).Checked() {
		t.Errorf("%s is not ticked", cardType)
	}
	if app.cardTypes.All().Checked() {
		t.Error("All Types is ticked even though one type is filtered")
	}
	if !app.mReaderFeedback.Checked() {
		t.Error("reader feedback is not ticked")
	}

	// And back to no filter at all.
	app.SyncPreferencesToMenu(nfcagent.Preferences{Mode: nfc.ModeReadWrite})
	if !app.cardTypes.All().Checked() || app.cardTypes.Item(cardType).Checked() {
		t.Error("clearing the stored card types did not restore All Types")
	}
}

func TestReaderFeedbackToggleReachesTheAgent(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	app.mReaderFeedback.Click()

	if !app.mReaderFeedback.Checked() {
		t.Error("the toggle is not ticked after clicking it")
	}
	if !agent.ReaderFeedback() {
		t.Error("the agent was not told about the change")
	}

	app.mReaderFeedback.Click()
	if app.mReaderFeedback.Checked() || agent.ReaderFeedback() {
		t.Error("clicking again did not turn the feedback back off")
	}
}

func TestOriginsMenuOffersBlockedOriginsAndAllowsThem(t *testing.T) {
	agent := newTestAgent()

	store, err := nfcagent.NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	agent = newTestAgentWith(nfcagent.Config{Origins: store})

	app, _ := newTestTray(t, agent)
	app.startOriginWatcher()

	// A refused page shows up as a one-click offer to allow it, put there by
	// the watcher rather than by reopening the menu.
	store.RecordBlocked("https://console.example")

	row := findOriginRow(t, app, "Allow console.example")
	row.Click()

	if !store.Allowed("console.example") {
		t.Fatal("the origin was not allowed")
	}

	// It is now an allowed origin, and clicking it revokes it again.
	allowed := findOriginRow(t, app, "console.example")
	if !allowed.Checked() {
		t.Error("an allowed origin is not ticked")
	}
	allowed.Click()

	if store.Allowed("console.example") {
		t.Fatal("the origin was not revoked")
	}
}

func TestOriginsAllowAnyToggle(t *testing.T) {
	agent := newTestAgent()

	store, err := nfcagent.NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	agent = newTestAgentWith(nfcagent.Config{Origins: store})

	app, _ := newTestTray(t, agent)

	app.mOriginAllowAny.Click()
	if !store.IsSessionAllowAny() || !app.mOriginAllowAny.Checked() {
		t.Fatal("the session escape hatch did not turn on")
	}

	app.mOriginAllowAny.Click()
	if store.IsSessionAllowAny() || app.mOriginAllowAny.Checked() {
		t.Fatal("the session escape hatch did not turn back off")
	}
}

// findOriginRow returns the visible origin row with the given label.
func findOriginRow(t *testing.T, app *App, title string) *traymenu.Item {
	t.Helper()

	for _, item := range app.origins.Items() {
		if item.Visible() && item.Title() == title {
			return item
		}
	}

	var shown []string
	for _, item := range app.origins.Items() {
		if item.Visible() {
			shown = append(shown, item.Title())
		}
	}
	t.Fatalf("no origin row titled %q; the menu shows %v", title, shown)
	return nil
}

func TestPairedDevicesMenuCountsAndRevokes(t *testing.T) {
	agent := newTestAgent()

	registry, err := nfcagent.NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent = newTestAgentWith(nfcagent.Config{Devices: registry})

	app, _ := newTestTray(t, agent)

	if app.mDevicesMenu.Title() != "Paired Devices (none)" {
		t.Errorf("title = %q, want %q", app.mDevicesMenu.Title(), "Paired Devices (none)")
	}
	if app.mRevokeAllDevices.Visible() {
		t.Error("Revoke all devices is offered with nothing paired")
	}

	if _, _, err := registry.Pair("Pixel", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	app.refreshDevicesMenu()

	if app.mDevicesMenu.Title() != "Paired Devices (1)" {
		t.Errorf("title = %q, want %q", app.mDevicesMenu.Title(), "Paired Devices (1)")
	}
	rows := app.pairedDevices.Rows()
	if len(rows) != 1 || rows[0].Title != "Pixel (android)" {
		t.Fatalf("rows = %v, want one row labelled %q", rows, "Pixel (android)")
	}

	app.pairedDevices.Items()[0].Click()

	if registry.Count() != 0 {
		t.Fatal("the device was not revoked")
	}
	if app.pairedDevices.Len() != 0 {
		t.Fatal("the revoked device is still on the menu")
	}
}

func TestRequirePairingRefusesToLockEveryoneOut(t *testing.T) {
	agent := newTestAgent()

	registry, err := nfcagent.NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent = newTestAgentWith(nfcagent.Config{Devices: registry})

	app, _ := newTestTray(t, agent)

	// Nothing is paired, so requiring pairing would refuse every device.
	app.mRequirePaired.Click()
	if app.mRequirePaired.Checked() || agent.RequirePairedDevice() {
		t.Fatal("pairing was required with no paired device to admit")
	}

	if _, _, err := registry.Pair("Pixel", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	app.mRequirePaired.Click()

	if !app.mRequirePaired.Checked() || !agent.RequirePairedDevice() {
		t.Fatal("pairing was not required once a device had paired")
	}
}

func TestAgentStateDrivesTheControls(t *testing.T) {
	app, _ := newTestTray(t, newTestAgent())

	app.showRunning()
	if app.mStatus.Title() != "Running" || app.mStart.Enabled() || !app.mStop.Enabled() {
		t.Fatalf("running: status %q, start enabled %v, stop enabled %v",
			app.mStatus.Title(), app.mStart.Enabled(), app.mStop.Enabled())
	}

	app.showStopped("Stopped")
	if app.mStatus.Title() != "Stopped" || !app.mStart.Enabled() || app.mStop.Enabled() {
		t.Fatalf("stopped: status %q, start enabled %v, stop enabled %v",
			app.mStatus.Title(), app.mStart.Enabled(), app.mStop.Enabled())
	}
}

// The tray writes to the same file the console does. A mode picked from a menu
// used to be forgotten at exit, which made where an operator clicked decide
// whether their choice survived.
func TestTrayModeAndFilterReachTheAgent(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	app.modes.Item(nfc.ModeReadOnly).Click()
	if got := agent.CurrentReaderMode(); got != nfc.ModeReadOnly {
		t.Errorf("mode = %v, want ModeReadOnly", got)
	}

	cardType := nfc.GetAllCardTypes()[0]
	app.cardTypes.Item(cardType).Click()
	if got := agent.CardTypeFilter(); len(got) != 1 || got[0] != cardType {
		t.Errorf("card types = %v, want [%s]", got, cardType)
	}

	app.cardTypes.All().Click()
	if got := agent.CardTypeFilter(); len(got) != 0 {
		t.Errorf("card types = %v, want none", got)
	}
}
