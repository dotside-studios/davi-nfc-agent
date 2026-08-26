package nfctest

import (
	"runtime"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestReader_NoGoroutineLeakAcrossStartStop guards against goroutine leaks in the
// reader lifecycle: polling, doing work, and stopping must leave the goroutine
// count back at a warmed-up baseline. It runs in write-only mode so the poll
// loop does not block on an undrained scan.
func TestReader_NoGoroutineLeakAcrossStartStop(t *testing.T) {
	cycle := func() {
		card := NTAG215("04A1B2C3D4E5F6").WithText("hi")
		r := NewEmulatedReader(t, card)
		r.SetMode(nfc.ModeWriteOnly)
		if _, err := r.WriteMessage(textMessage("x"),
			nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
			t.Fatalf("write: %v", err)
		}
		time.Sleep(30 * time.Millisecond) // let a few poll ticks run
		r.Stop()                          // ends the readers and joins their workers
	}

	cycle() // warm-up absorbs one-time initialization goroutines
	baseline := settledGoroutines()

	for i := 0; i < 3; i++ {
		cycle()
	}

	if after := settledGoroutines(); after > baseline {
		t.Errorf("goroutine count grew across start/stop cycles: baseline=%d after=%d (possible leak)", baseline, after)
	}
}

// settledGoroutines returns the goroutine count once it has stopped changing,
// giving transient (non-tracked) poll goroutines time to wind down.
// settledGoroutines waits for the goroutine count to stop moving.
//
// It has to hold still for longer than one polling interval. A poll in flight
// when the reader stops is deliberately not joined, so it sleeps out its
// interval before ending, and a count that includes one looks settled to any
// check shorter than that.
func settledGoroutines() int {
	const stableSamples = 15 // 150ms, longer than nfc.DefaultPollingInterval

	prev, stable := -1, 0
	for i := 0; i < 400; i++ {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			if stable++; stable >= stableSamples {
				return n
			}
		} else {
			prev, stable = n, 0
		}
		time.Sleep(10 * time.Millisecond)
	}
	return prev
}
