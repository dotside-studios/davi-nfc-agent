package agent

import (
	"context"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
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
	notify := p.clientChanges.Emit

	got := -1
	sub := p.OnClientsChange(func(clients int) { got = clients })
	defer sub.Disconnect()

	notify(3)
	if got != 3 {
		t.Errorf("a subscriber connected after the callback was taken saw %d clients, want 3", got)
	}
}

// A subscriber follows the plugin rather than the server behind it, so
// replacing that server does not silently drop it.
func TestAClientSubscriberSurvivesTheServerBehindIt(t *testing.T) {
	p := &ServerPlugin{}
	a := serverAgent(t, p)

	got := -1
	sub := p.OnClientsChange(func(clients int) { got = clients })
	defer sub.Disconnect()

	// The component rather than the agent, so nothing binds a port: what is
	// under test is which server the subscription is attached to.
	clients := &clientsComponent{plugin: p, agent: a}
	if err := clients.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := p.serving()

	if err := clients.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := clients.Start(context.Background()); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	t.Cleanup(func() { _ = clients.Stop() })

	if p.serving() == first || p.serving() == nil {
		t.Fatal("the server was not replaced")
	}

	p.clientChanges.Emit(2)
	if got != 2 {
		t.Errorf("after the server was replaced the subscriber saw %d, want 2", got)
	}

	sub.Disconnect()
	p.clientChanges.Emit(5)
	if got != 2 {
		t.Error("a disconnected subscriber was still called")
	}
}
