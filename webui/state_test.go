package webui

import (
	"encoding/json"
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
