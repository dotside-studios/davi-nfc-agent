package webui

import (
	"encoding/json"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"strings"
	"testing"
	"time"
)

// The console reads the snapshot by JSON key. A wire type that loses its tags
// still compiles, still marshals and still passes a status-code test — it just
// renders every field blank, which is how the clients table shipped empty once.
func TestSnapshotKeysAreLowerCamel(t *testing.T) {
	host := newFakeHost()
	host.seedDevices()
	host.clients = []Client{{
		ID: "c1", Origin: "https://app.example.com", RemoteAddr: "127.0.0.1:5000",
		UserAgent: "Mozilla/5.0", ConnectedAt: time.Now(), Writes: 2, Locks: 1,
	}}

	console := New(Config{Host: host, Name: "davi-nfc-agent", Version: "test"})

	body, err := json.Marshal(console.buildState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)

	for _, key := range []string{
		`"id":"c1"`,
		`"origin":"https://app.example.com"`,
		`"remoteAddr":"127.0.0.1:5000"`,
		`"userAgent":"Mozilla/5.0"`,
		`"connectedAt":`,
		`"writes":2`,
		`"locks":1`,
		`"id":"dev-1"`,
		`"name":"Ned's iPhone"`,
		`"platform":"iOS 17.4"`,
		`"pairedAt":`,
		`"online":true`,
	} {
		if !strings.Contains(got, key) {
			t.Errorf("snapshot is missing %s", key)
		}
	}

	// An exported field marshalled under its Go name is the failure mode.
	for _, key := range []string{`"ID"`, `"Origin"`, `"RemoteAddr"`, `"ConnectedAt"`, `"Writes"`} {
		if strings.Contains(got, key) {
			t.Errorf("%s marshalled under its Go name; the wire type is missing a json tag", key)
		}
	}
}

// The snapshot carries a preference once, in Settings, taken from the agent. A
// second copy beside it, such as the reader's own mode, can disagree with it,
// and a console showing read-only while the reader writes costs a card.
func TestPreferencesComeOnlyFromTheAgentsSettings(t *testing.T) {
	host := newFakeHost()
	host.settings = Preferences{
		Mode:                nfc.ModeNameReadOnly,
		DevicePath:          "ACS ACR1252U 01 00",
		RequirePairedDevice: true,
	}

	console := New(Config{Host: host, Name: "davi-nfc-agent", Version: "test"})
	state := console.buildState()

	if state.Settings.Mode != nfc.ModeNameReadOnly {
		t.Errorf("snapshot mode = %q, want %q", state.Settings.Mode, nfc.ModeNameReadOnly)
	}
	if !state.Settings.RequirePairedDevice {
		t.Error("the snapshot does not report the requirement the agent is enforcing")
	}
	// The blocks a preference used to be repeated in, and where a second copy
	// would come back.
	for name, block := range map[string]any{"reader": state.Reader, "security": state.Security} {
		body, err := json.Marshal(block)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		for _, key := range []string{`"mode":`, `"devicePath":`, `"requirePairedDevice":`, `"cardTypes":`} {
			if strings.Contains(string(body), key) {
				t.Errorf("%s repeats %s, which belongs to settings", name, key)
			}
		}
	}
}
