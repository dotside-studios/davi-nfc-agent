package main

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func newTestAgent() *Agent {
	return &Agent{
		Logger:           log.New(io.Discard, "", 0),
		AllowedCardTypes: make(map[string]bool),
	}
}

// newTestTray builds the real tray menu on a fake driver, so it can be read and
// clicked with no desktop involved.
func newTestTray(t *testing.T, agent *Agent) (*SystrayApp, *traymenu.Fake) {
	t.Helper()

	fake := traymenu.NewFake()
	app := newSystrayApp(agent, "", 0, nil, fake)
	t.Cleanup(app.menu.Close)
	app.setupUI()
	return app, fake
}

// titles lists the top level as the user reads it, separators included. The
// control center entry is left out: a -tags nowebui build has no console, so
// the entry is not there to read. TestConsoleEntry covers it instead.
func titles(fake *traymenu.Fake) []string {
	var out []string
	for _, item := range fake.Items() {
		if item.IsSeparator() {
			out = append(out, "----")
			continue
		}
		if item.Title() == "Open Control Center" {
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
		"Server URLs",
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
		"Trust This Agent in Browsers",
		"----",
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

func TestConsoleEntry(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	// Nothing for the entry to open, so it must not be offered.
	if item := fake.Find("Open Control Center"); item != nil && item.Visible() {
		t.Error("the control center entry is offered with no console behind it")
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

func TestPairingAndSecretEntriesHiddenWhenUnconfigured(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	for _, title := range []string{"Pairing PIN: --", "  Copy Pairing PIN", "  Regenerate Pairing PIN",
		"API Secret: hidden", "  Copy API Secret", "  Regenerate API Secret"} {
		item := fake.Find("Server URLs", title)
		if item == nil {
			t.Fatalf("%q is missing from the URLs submenu", title)
		}
		if item.Visible() {
			t.Errorf("%q is shown even though it has nothing behind it", title)
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
	filter := app.cardTypeFilters[cardType]

	filter.Click()

	if !filter.Checked() {
		t.Error("the clicked card type is not ticked")
	}
	if app.mFilterAll.Checked() {
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
	filter := app.cardTypeFilters[cardType]

	filter.Click()
	filter.Click()

	if filter.Checked() {
		t.Error("the card type is still ticked after a second click")
	}
	if !app.mFilterAll.Checked() {
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
	app.cardTypeFilters[cardType].Click()

	app.mFilterAll.Click()

	if app.cardTypeFilters[cardType].Checked() {
		t.Error("an individual filter is still ticked under All Types")
	}
	if !app.mFilterAll.Checked() {
		t.Error("All Types is not ticked")
	}
	if agent.AllowedCardTypesLength() != len(nfc.GetAllCardTypes()) {
		t.Error("All Types did not allow every card type on the agent")
	}
}

func TestModeMenuRevertsWithoutAReader(t *testing.T) {
	app, _ := newTestTray(t, newTestAgent())

	// No reader to apply it to, so the tick goes back where it was.
	app.modes.Item(nfc.ModeWriteOnly).Click()

	if got, _ := app.modes.Value(); got != nfc.ModeReadWrite {
		t.Errorf("mode = %v, want ModeReadWrite", got)
	}
	if app.mModeMenu.Title() != "Mode: Read/Write" {
		t.Errorf("mode menu title = %q, want %q", app.mModeMenu.Title(), "Mode: Read/Write")
	}
}

func TestSyncSettingsToMenu(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	cardType := nfc.GetAllCardTypes()[0]
	app.syncSettingsToMenu(settings.Settings{
		Mode:           settings.ModeWriteOnly,
		CardTypes:      []string{cardType},
		ReaderFeedback: true,
	})

	if got, _ := app.modes.Value(); got != nfc.ModeWriteOnly {
		t.Errorf("mode = %v, want ModeWriteOnly", got)
	}
	if app.mModeMenu.Title() != "Mode: Write Only" {
		t.Errorf("mode menu title = %q, want %q", app.mModeMenu.Title(), "Mode: Write Only")
	}
	if !app.cardTypeFilters[cardType].Checked() {
		t.Errorf("%s is not ticked", cardType)
	}
	if app.mFilterAll.Checked() {
		t.Error("All Types is ticked even though one type is filtered")
	}
	if !app.mReaderFeedback.Checked() {
		t.Error("reader feedback is not ticked")
	}

	// And back to no filter at all.
	app.syncSettingsToMenu(settings.Settings{Mode: settings.ModeReadWrite})
	if !app.mFilterAll.Checked() || app.cardTypeFilters[cardType].Checked() {
		t.Error("clearing the stored card types did not restore All Types")
	}
}

func TestReaderFeedbackTogglePersists(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)

	store, err := settings.New(t.TempDir())
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	app.AttachSettings(store)

	app.mReaderFeedback.Click()

	if !app.mReaderFeedback.Checked() {
		t.Error("the toggle is not ticked after clicking it")
	}
	if !agent.ReaderFeedback {
		t.Error("the agent was not told about the change")
	}
	if !store.Get().ReaderFeedback {
		t.Error("the preference was not saved")
	}

	app.mReaderFeedback.Click()
	if app.mReaderFeedback.Checked() || agent.ReaderFeedback || store.Get().ReaderFeedback {
		t.Error("clicking again did not turn the feedback back off")
	}
}

func TestOriginsMenuOffersBlockedOriginsAndAllowsThem(t *testing.T) {
	agent := newTestAgent()

	store, err := NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	agent.Origins = store

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

	store, err := NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	agent.Origins = store

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
func findOriginRow(t *testing.T, app *SystrayApp, title string) *traymenu.Item {
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

	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent.Devices = registry

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

	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent.Devices = registry

	app, _ := newTestTray(t, agent)

	// Nothing is paired, so requiring pairing would refuse every device.
	app.mRequirePaired.Click()
	if app.mRequirePaired.Checked() || agent.RequirePairedDevice {
		t.Fatal("pairing was required with no paired device to admit")
	}

	if _, _, err := registry.Pair("Pixel", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	app.mRequirePaired.Click()

	if !app.mRequirePaired.Checked() || !agent.RequirePairedDevice {
		t.Fatal("pairing was not required once a device had paired")
	}
}
