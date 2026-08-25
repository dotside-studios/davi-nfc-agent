package tray

import (
	"testing"

	nfcagent "github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The tray used to sync its menus only from its own clicks, so a mode picked in
// the console left the tick where the operator last put it.
func TestAPreferenceChangedElsewhereRedrawsTheMenu(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)
	app.subscribe()

	agent.SetReaderMode(nfc.ModeWriteOnly)

	if got, _ := app.modes.Value(); got != nfc.ModeWriteOnly {
		t.Errorf("the menu shows %v, want %v", got, nfc.ModeWriteOnly)
	}
}

// A device paired from the console or over the pairing server shows up without
// the operator reopening the menu.
func TestADevicePairedElsewhereRedrawsTheMenu(t *testing.T) {
	registry, err := nfcagent.NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent := newTestAgentWith(nfcagent.Config{Devices: registry})
	app, _ := newTestTray(t, agent)
	app.subscribe()

	if _, _, err := registry.Pair("phone", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	rows := app.pairedDevices.Rows()
	if len(rows) != 1 || rows[0].Title != "phone (android)" {
		t.Errorf("the menu shows %v, want the paired device", rows)
	}
}

// The card lines follow the scans and the reader's status, which is what
// replaced polling the last card twice a second.
func TestTheCardLinesFollowTheReader(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)
	app.subscribe()

	agent.Events().Tag.Emit(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
	if got := app.mCardUID.Title(); got != "Card UID: 04A1B2C3" {
		t.Errorf("card line is %q, want the scanned UID", got)
	}

	agent.Events().Reader.Emit(nfc.DeviceStatus{Connected: true, CardPresent: false})
	if got := app.mCardUID.Title(); got != "Card UID: None" {
		t.Errorf("card line is %q after the card left the reader, want none", got)
	}
}

// A stop the tray did not ask for still reaches the menu.
func TestAStopElsewhereReachesTheControls(t *testing.T) {
	agent := newTestAgent()
	app, _ := newTestTray(t, agent)
	app.subscribe()

	agent.Events().State.Emit(nfcagent.StateRunning)
	if app.mStatus.Title() != "Running" {
		t.Fatalf("status is %q, want Running", app.mStatus.Title())
	}

	agent.Events().Tag.Emit(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
	agent.Events().State.Emit(nfcagent.StateStopped)

	if app.mStatus.Title() != "Stopped" || !app.mStart.Enabled() || app.mStop.Enabled() {
		t.Errorf("stopped: status %q, start enabled %v, stop enabled %v",
			app.mStatus.Title(), app.mStart.Enabled(), app.mStop.Enabled())
	}
	if got := app.mCardType.Title(); got != "Card Type: None" {
		t.Errorf("card type is %q on a stopped agent, want none", got)
	}
}

// An origin allowed from the console shows as allowed in the tray, without the
// operator reopening the menu.
func TestAnOriginAllowedElsewhereRedrawsTheMenu(t *testing.T) {
	store, err := nfcagent.NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	agent := newTestAgentWith(nfcagent.Config{Origins: store})
	app, _ := newTestTray(t, agent)
	app.subscribe()

	if err := store.Allow("console.example"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	row := findOriginRow(t, app, "console.example")
	if !row.Checked() {
		t.Error("the allowed origin is not ticked in the menu")
	}
}
