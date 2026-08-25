package event_test

import (
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/event"
)

func TestSignalEmitCallsHandlersInOrder(t *testing.T) {
	var sig event.Signal[int]

	var got []string
	sig.Connect(func(v int) { got = append(got, "first") })
	sig.Connect(func(v int) { got = append(got, "second") })

	sig.Emit(7)

	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("handlers ran %v, want [first second]", got)
	}
}

func TestSignalEmitPassesValue(t *testing.T) {
	var sig event.Signal[string]

	var got string
	sig.Connect(func(v string) { got = v })
	sig.Emit("device-2")

	if got != "device-2" {
		t.Fatalf("handler saw %q, want %q", got, "device-2")
	}
}

func TestZeroSignalEmitsToNobody(t *testing.T) {
	var sig event.Signal[struct{}]
	sig.Emit(struct{}{}) // must not panic
	if sig.Len() != 0 {
		t.Fatalf("Len = %d, want 0", sig.Len())
	}
}

func TestSignalDisconnectStopsHandler(t *testing.T) {
	var sig event.Signal[int]

	calls := 0
	conn := sig.Connect(func(int) { calls++ })

	sig.Emit(1)
	conn.Disconnect()
	sig.Emit(2)

	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
	if sig.Len() != 0 {
		t.Fatalf("Len = %d after disconnect, want 0", sig.Len())
	}
}

func TestSignalDisconnectIsIdempotentAndNilSafe(t *testing.T) {
	var sig event.Signal[int]

	conn := sig.Connect(func(int) {})
	conn.Disconnect()
	conn.Disconnect() // must not panic or remove someone else's slot

	other := sig.Connect(func(int) {})
	conn.Disconnect()
	if sig.Len() != 1 {
		t.Fatalf("Len = %d, want 1: a repeated Disconnect removed the wrong handler", sig.Len())
	}
	other.Disconnect()

	var nilConn *event.Connection
	nilConn.Disconnect() // must not panic
}

func TestSignalConnectNilHandlerConnectsNothing(t *testing.T) {
	var sig event.Signal[int]

	conn := sig.Connect(nil)
	if sig.Len() != 0 {
		t.Fatalf("Len = %d, want 0", sig.Len())
	}
	conn.Disconnect() // must not panic
	sig.Emit(1)
}

func TestSignalConnectOnceRunsOnce(t *testing.T) {
	var sig event.Signal[int]

	calls := 0
	sig.ConnectOnce(func(int) { calls++ })
	survivor := 0
	sig.Connect(func(int) { survivor++ })

	sig.Emit(1)
	sig.Emit(2)

	if calls != 1 {
		t.Fatalf("once handler called %d times, want 1", calls)
	}
	if survivor != 2 {
		t.Fatalf("lasting handler called %d times, want 2", survivor)
	}
	if sig.Len() != 1 {
		t.Fatalf("Len = %d, want 1", sig.Len())
	}
}

func TestSignalHandlerMayConnectDuringEmit(t *testing.T) {
	var sig event.Signal[int]

	late := 0
	sig.ConnectOnce(func(int) {
		sig.Connect(func(int) { late++ })
	})

	sig.Emit(1)
	if late != 0 {
		t.Fatalf("handler connected during Emit saw that emission")
	}

	sig.Emit(2)
	if late != 1 {
		t.Fatalf("handler connected during Emit ran %d times on the next one, want 1", late)
	}
}

func TestSignalHandlerMayDisconnectItselfDuringEmit(t *testing.T) {
	var sig event.Signal[int]

	calls := 0
	var conn *event.Connection
	conn = sig.Connect(func(int) {
		calls++
		conn.Disconnect()
	})

	sig.Emit(1)
	sig.Emit(2)

	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
}

func TestSignalHandlerMayEmitDuringEmit(t *testing.T) {
	var sig event.Signal[int]

	var seen []int
	sig.Connect(func(v int) {
		seen = append(seen, v)
		if v == 1 {
			sig.Emit(2)
		}
	})

	sig.Emit(1)

	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("re-entrant emit produced %v, want [1 2]", seen)
	}
}

func TestSignalClear(t *testing.T) {
	var sig event.Signal[int]

	sig.Connect(func(int) { t.Error("cleared handler ran") })
	sig.Clear()

	if sig.Len() != 0 {
		t.Fatalf("Len = %d after Clear, want 0", sig.Len())
	}
	sig.Emit(1)
}

func TestSignalConcurrentConnectEmitDisconnect(t *testing.T) {
	var sig event.Signal[int]

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				conn := sig.Connect(func(int) {})
				sig.Emit(j)
				conn.Disconnect()
			}
		}()
	}
	wg.Wait()

	if sig.Len() != 0 {
		t.Fatalf("Len = %d, want 0", sig.Len())
	}
}

// A channel is the escape hatch for a consumer that drains on its own terms.
func TestChannelCarriesWhatIsEmitted(t *testing.T) {
	var sig event.Signal[int]

	values, stop := sig.Channel(4)
	sig.Emit(1)
	sig.Emit(2)

	if got := <-values; got != 1 {
		t.Errorf("first value is %d, want 1", got)
	}
	if got := <-values; got != 2 {
		t.Errorf("second value is %d, want 2", got)
	}

	stop()
	sig.Emit(3)

	select {
	case got := <-values:
		t.Errorf("a stopped channel carried %d", got)
	default:
	}
}

// A reader that stops draining must not stall whoever is emitting, which is a
// reader poll loop or a WebSocket read loop.
func TestAFullChannelDropsRatherThanBlocking(t *testing.T) {
	var sig event.Signal[int]

	values, stop := sig.Channel(1)
	defer stop()

	for i := 0; i < 100; i++ {
		sig.Emit(i) // would deadlock if a full buffer blocked
	}

	if got := <-values; got != 0 {
		t.Errorf("the buffered value is %d, want the first one emitted", got)
	}
	if len(values) != 0 {
		t.Errorf("%d values are queued, want the rest dropped", len(values))
	}
}
