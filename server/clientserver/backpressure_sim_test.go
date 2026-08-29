package clientserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/gorilla/websocket"
)

// TestBackpressure_StalledClientDoesNotBlockProducer is the worst case for the
// client broadcast path: one connected client stops reading its socket (a hung
// browser tab, a wedged app, a client paused in a debugger). Every scan is
// broadcast to every client synchronously on the producer's own goroutine, so a
// single client whose socket send buffer has filled must not be able to block
// that goroutine — otherwise one stuck client freezes scan delivery for every
// other client and stalls the reader that produced the scan.
func TestBackpressure_StalledClientDoesNotBlockProducer(t *testing.T) {
	s := newTestServer(nil)

	// A client that connects and then never reads. Its receive buffer, and the
	// server's send buffer for it, fill after enough scans.
	stalled := dial(t, s, "https://app.example.com")
	_ = stalled // deliberately never read
	waitFor(t, func() bool { return s.ClientCount() == 1 })

	card := nfc.NewCard(nfc.NewMockTag("04DEADBEEF01"))

	done := make(chan struct{})
	go func() {
		// Far more than any socket buffer can hold, so a blocking send would wedge
		// here well before finishing.
		for i := 0; i < 20000; i++ {
			s.Broadcast(nfc.NFCData{Card: card})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Broadcast blocked on a client that stopped reading — one stalled client freezes scan delivery for the whole agent")
	}
}

// TestBackpressure_StalledClientDoesNotStarveHealthyOne checks the other side of
// the same property: with a stalled client connected, a healthy client must keep
// receiving broadcasts rather than being starved behind the stalled one.
func TestBackpressure_StalledClientDoesNotStarveHealthyOne(t *testing.T) {
	s := newTestServer(nil)

	stalled := dial(t, s, "https://app.example.com")
	_ = stalled // never read
	waitFor(t, func() bool { return s.ClientCount() == 1 })

	healthy := dial(t, s, "https://app.example.com")
	waitFor(t, func() bool { return s.ClientCount() == 2 })

	// Drain the healthy client continuously.
	got := make(chan string, 1024)
	go func() {
		for {
			var msg map[string]any
			if err := healthy.ReadJSON(&msg); err != nil {
				return
			}
			payload, _ := msg["payload"].(map[string]any)
			if uid, _ := payload["uid"].(string); uid != "" {
				select {
				case got <- uid:
				default:
				}
			}
		}
	}()

	// Keep the stalled client's buffer full while broadcasting a marker the
	// healthy client should still receive.
	go func() {
		card := nfc.NewCard(nfc.NewMockTag("04DEADBEEF02"))
		for i := 0; i < 20000; i++ {
			s.Broadcast(nfc.NFCData{Card: card})
		}
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case uid := <-got:
			if uid == "04DEADBEEF02" {
				return // the healthy client is flowing despite the stalled one
			}
		case <-deadline:
			t.Fatal("healthy client was starved behind a stalled client")
		}
	}
}

// TestBackpressure_ConnectDisconnectChurnDuringBroadcast hammers the client
// registry: clients connect and disconnect continuously while scans are
// broadcast at full rate. Broadcasts iterate the client set under a read lock
// while connects and disconnects mutate it under the write lock, and each new
// client's writer goroutine starts and stops in the middle of it all. The point
// is that this churn stays free of races, panics, and deadlocks (run under
// -race), and that the server settles to zero clients afterwards.
func TestBackpressure_ConnectDisconnectChurnDuringBroadcast(t *testing.T) {
	s := newTestServer(nil)
	ts := httptest.NewServer(s)
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	header := http.Header{}
	header.Set("Origin", "https://app.example.com")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Broadcast continuously.
	wg.Add(1)
	go func() {
		defer wg.Done()
		card := nfc.NewCard(nfc.NewMockTag("04CADE7A9901"))
		for {
			select {
			case <-stop:
				return
			default:
				s.Broadcast(nfc.NFCData{Card: card})
			}
		}
	}()

	// Churn connections: dial, briefly read, close.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				select {
				case <-stop:
					return
				default:
				}
				conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
				if err != nil {
					continue
				}
				_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
				var msg map[string]any
				_ = conn.ReadJSON(&msg)
				_ = conn.Close()
			}
		}()
	}

	// Let the churn run, then stop the broadcaster and wait for the dialers.
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The registry must drain back to empty once every client has gone.
	waitFor(t, func() bool { return s.ClientCount() == 0 })
}
