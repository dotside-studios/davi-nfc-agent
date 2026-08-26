package agent

import (
	"net/http"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
)

// The connected count is the server's, and the preferences are the agent's, so
// something following one is not woken by the other. The console takes both,
// since either changes what an open page shows.
func TestAPreferenceChangeDoesNotLookLikeAClientChange(t *testing.T) {
	p := &ServerPlugin{}
	a := serverAgent(t, p)

	clients, preferences := 0, 0
	p.OnClientsChange(func(int) { clients++ })
	a.Events().Preferences.Connect(func(Preferences) { preferences++ })

	// Preferences is a Property, so connecting replayed the current value.
	preferences = 0

	a.SetReaderMode(nfc.ModeReadOnly)

	if preferences != 1 {
		t.Errorf("preference hook ran %d times, want 1", preferences)
	}
	if clients != 0 {
		t.Errorf("the client hook ran %d times for a preference change, want 0", clients)
	}
}

// The client server captures one callback when it is built, so a subscriber
// that connects after the agent starts must still be called. Subscribers were
// once snapshotted there, which left a late one seeing preference changes but
// no client ones.
func TestALateClientSubscriberIsStillCalled(t *testing.T) {
	p := &ServerPlugin{}
	a := serverAgent(t, p)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// What the client server was handed, taken before any subscriber exists.
	notify := p.Events().Clients.Emit

	got := -1
	sub := p.OnClientsChange(func(clients int) { got = clients })
	defer sub.Disconnect()

	notify(3)
	if got != 3 {
		t.Errorf("a subscriber connected after the callback was taken saw %d clients, want 3", got)
	}
}

// A subscriber connects to the plugin, so a build that supplied its own client
// server is reported on the same way as one the plugin built.
func TestASubscriberFollowsTheServerTheBuildSupplied(t *testing.T) {
	supplied := clientserver.New(clientserver.Config{})
	p := &ServerPlugin{ServeMode: map[string]http.Handler{server.ModeClient: supplied}}

	got := -1
	sub := p.OnClientsChange(func(clients int) { got = clients })
	defer sub.Disconnect()

	serverAgent(t, p).Activate(nil) //nolint:errcheck // activation is asserted below

	if p.serving() != supplied {
		t.Fatal("the plugin replaced the server the build supplied")
	}

	// What the server reports reaches a subscriber that connected to the
	// plugin before it was wired to one.
	supplied.OnClientsChange(func(int) {})
	p.Events().Clients.Emit(2)
	if got != 2 {
		t.Errorf("the subscriber saw %d, want 2", got)
	}

	sub.Disconnect()
	p.Events().Clients.Emit(5)
	if got != 2 {
		t.Error("a disconnected subscriber was still called")
	}
}
