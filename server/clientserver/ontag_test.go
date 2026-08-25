package clientserver

import (
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// startWithOnTag builds a server wired to an OnTag observer, returning it so a
// test can hand it scans the way the agent's pumps do.
func startWithOnTag(t *testing.T, onTag func(nfc.NFCData)) *Server {
	t.Helper()
	return New(Config{AllowedOrigins: []string{"*"}, OnTag: onTag})
}

// TestOnTagObservesScans is the contract docs/custom-builds.md documents: an embedder
// sees every scan without connecting a WebSocket client of its own.
func TestOnTagObservesScans(t *testing.T) {
	var mu sync.Mutex
	var seen []string

	srv := startWithOnTag(t, func(data nfc.NFCData) {
		mu.Lock()
		defer mu.Unlock()
		if data.Card != nil {
			seen = append(seen, data.Card.UID)
		}
	})

	for _, uid := range []string{"04112233", "04AABBCC"} {
		srv.Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag(uid))})
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
	observed := make(chan struct{}, 1)
	s := New(Config{
		AllowedOrigins: []string{"*"},
		OnTag: func(nfc.NFCData) {
			select {
			case observed <- struct{}{}:
			default:
			}
		},
	})

	conn := dial(t, s, "https://app.example.com")

	s.Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04DEADBE"))})

	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("OnTag was never called")
	}

	// The scan the observer saw is still the client's to receive.
	msg := readResponse(t, conn)
	payload, _ := msg["payload"].(map[string]any)
	if uid, _ := payload["uid"].(string); uid != "04DEADBE" {
		t.Errorf("the client received %#v, want the scan the observer saw", msg)
	}
}

// TestNilOnTagIsFine keeps the zero-config path working: most callers set no
// observer, and the loop must not dereference one.
func TestNilOnTagIsFine(t *testing.T) {
	srv := startWithOnTag(t, nil)

	// Nothing to assert beyond not panicking.
	srv.Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04000001"))})
}
