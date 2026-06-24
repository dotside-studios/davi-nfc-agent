package nfctest

import (
	"runtime"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestReader_NoGoroutineLeakAcrossStartStop guards against goroutine leaks in the
// reader lifecycle: starting the worker, doing work, and stopping must leave the
// goroutine count back at a warmed-up baseline. The reader runs in write-only
// mode so the poll loop doesn't block on the (undrained) data channel.
func TestReader_NoGoroutineLeakAcrossStartStop(t *testing.T) {
	cycle := func() {
		card := NTAG215("04A1B2C3D4E5F6").WithText("hi")
		r := NewEmulatedReader(t, card)
		r.SetMode(nfc.ModeWriteOnly)
		r.Start()
		if _, err := r.WriteMessageWithResult(textMessage("x"),
			nfc.WriteOptions{Overwrite: true, Index: -1}); err != nil {
			t.Fatalf("write: %v", err)
		}
		time.Sleep(30 * time.Millisecond) // let a few poll ticks run
		r.Stop()                          // closes stopChan and joins the worker
		r.Close()
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
func settledGoroutines() int {
	prev := -1
	for i := 0; i < 200; i++ {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
		time.Sleep(10 * time.Millisecond)
	}
	return prev
}
