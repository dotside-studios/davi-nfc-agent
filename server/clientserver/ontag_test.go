package clientserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// startWithOnTag builds a server wired to an OnTag observer and starts the
// bridge listeners, returning the bridge so a test can push scans through it.
func startWithOnTag(t *testing.T, onTag func(nfc.NFCData)) *server.ServerBridge {
	t.Helper()

	bridge := server.NewServerBridge()
	s := New(Config{AllowedOrigins: []string{"*"}, OnTag: onTag}, bridge)
	s.StartBackground(context.Background())
	t.Cleanup(s.Stop)
	return bridge
}

// TestOnTagObservesScans is the contract docs/custom-builds.md documents: an embedder
// sees every scan without connecting a WebSocket client of its own.
func TestOnTagObservesScans(t *testing.T) {
	var mu sync.Mutex
	var seen []string

	bridge := startWithOnTag(t, func(data nfc.NFCData) {
		mu.Lock()
		defer mu.Unlock()
		if data.Card != nil {
			seen = append(seen, data.Card.UID)
		}
	})

	for _, uid := range []string{"04112233", "04AABBCC"} {
		if !bridge.SendTagData(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag(uid))}) {
			t.Fatalf("bridge refused tag %s", uid)
		}
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 2
	})

	mu.Lock()
	defer mu.Unlock()
	if seen[0] != "04112233" || seen[1] != "04AABBCC" {
		t.Errorf("observed %v, want the two scans in order", seen)
	}
}

// TestOnTagStillBroadcasts guards the "observes rather than intercepts" half of
// the contract: the client server must go on serving the scan to its clients.
func TestOnTagStillBroadcasts(t *testing.T) {
	bridge := server.NewServerBridge()
	observed := make(chan struct{}, 1)
	s := New(Config{
		AllowedOrigins: []string{"*"},
		OnTag: func(nfc.NFCData) {
			select {
			case observed <- struct{}{}:
			default:
			}
		},
	}, bridge)
	s.StartBackground(context.Background())
	t.Cleanup(s.Stop)

	dial(t, s, "https://app.example.com")

	if !bridge.SendTagData(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04DEADBE"))}) {
		t.Fatal("bridge refused the tag")
	}

	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("OnTag was never called")
	}

	// The scan the observer saw is still the client's to receive.
	waitFor(t, func() bool {
		card := s.GetLastCard()
		return card != nil && card.UID == "04DEADBE"
	})
}

// TestNilOnTagIsFine keeps the zero-config path working: most callers set no
// observer, and the loop must not dereference one.
func TestNilOnTagIsFine(t *testing.T) {
	bridge := startWithOnTag(t, nil)

	if !bridge.SendTagData(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04000001"))}) {
		t.Fatal("bridge refused the tag")
	}
	// Nothing to assert beyond not panicking; the send is drained by the same
	// loop that would have called the observer.
	time.Sleep(50 * time.Millisecond)
}
