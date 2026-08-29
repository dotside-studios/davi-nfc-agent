package nfctest

import (
	"context"
	"sync"
	"testing"
	"time"
)

// These simulations target the agent's operation-serialization machinery — the
// part that decides what happens when a caller gives up on an operation the
// hardware is still performing, when two callers want the same reader at once,
// when the agent shuts down mid-operation, and when readers are added and dropped
// while operations run. A PC/SC exchange cannot be interrupted, so an operation
// whose caller has walked away keeps running and keeps the reader to itself; the
// contract is that this never wedges the reader, never lets a second operation
// drive the same tag concurrently, and never bleeds one reader's load onto
// another. The Slow card models an exchange still in flight when its caller times
// out. Run these under -race.

// TestConcurrencySim_AbandonedOperationHoldsThenRecovers: a caller with a short
// deadline gives up on a slow write; the write keeps running and holds the
// reader. A second impatient caller must not get in while it runs, and a patient
// caller must succeed once it finishes — the reader recovers rather than wedging.
func TestConcurrencySim_AbandonedOperationHoldsThenRecovers(t *testing.T) {
	card := NTAG215("045100510051AA").WithText("seed").Slow(5 * time.Millisecond)
	reader := NewEmulatedReader(t, card)

	// The write cannot finish inside 50ms; the caller gives up but the operation
	// keeps running and holds the reader's operation slot.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel1()
	if _, err := reader.Supervisor.WriteMessage(ctx1, "", textMessage("abandoned"), overwrite); err == nil {
		t.Fatal("expected the short-deadline write to be abandoned while it was still running")
	}

	// A second caller with a short deadline cannot acquire the reader while the
	// abandoned operation still holds it — it must be refused, not run in parallel
	// against the same tag.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if _, err := reader.Supervisor.WriteMessage(ctx2, "", textMessage("impatient"), overwrite); err == nil {
		t.Error("a second operation ran while an abandoned operation still held the reader")
	}

	// A patient caller waits for the abandoned operation to complete and then
	// succeeds: the reader is usable again.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()
	res, err := reader.Supervisor.WriteMessage(ctx3, "", textMessage("recovered"), overwrite)
	if err != nil {
		t.Fatalf("reader did not recover after an abandoned operation: %v", err)
	}
	if !res.Verified {
		t.Errorf("post-recovery write should verify, got %+v", res)
	}
}

// TestConcurrencySim_StuckLaneDoesNotBlockOthers: one lane is pinned by an
// abandoned, still-running operation. Another lane must keep serving promptly —
// per-reader operation slots must isolate lanes, so one stuck reader cannot stall
// the floor.
func TestConcurrencySim_StuckLaneDoesNotBlockOthers(t *testing.T) {
	lanes := NewEmulatedLanes(t, "slow", "fast")
	lanes.Present("slow", NTAG215(uidFor("slow")).WithText("seed").Slow(10*time.Millisecond))
	lanes.Present("fast", NTAG215(uidFor("fast")).WithText("seed"))

	// Pin the slow lane: a caller gives up but the operation keeps running and
	// holds that reader for well over a second.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = lanes.Supervisor.WriteMessage(ctx, "slow", textMessage("stuck"), overwrite)
	}()
	// Make sure the slow operation is in flight and already abandoned.
	time.Sleep(120 * time.Millisecond)

	start := time.Now()
	res, err := lanes.Write("fast", textMessage("served"), overwrite)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("the fast lane should serve while the slow lane is stuck: %v", err)
	}
	if !res.Verified {
		t.Errorf("fast-lane write should verify, got %+v", res)
	}
	if elapsed > time.Second {
		t.Errorf("fast lane took %v while the slow lane was stuck — lanes are not isolated", elapsed)
	}
}

// TestConcurrencySim_ShutdownDuringInFlightWrites: the agent is stopped while
// writes are in flight across lanes. Stop must return in bounded time (it may
// wait for the running operations, but must not wedge), with no race or panic.
func TestConcurrencySim_ShutdownDuringInFlightWrites(t *testing.T) {
	lanes := NewEmulatedLanes(t, "a", "b")
	lanes.Present("a", NTAG215(uidFor("a")).WithText("seed").Slow(3*time.Millisecond))
	lanes.Present("b", NTAG215(uidFor("b")).WithText("seed").Slow(3*time.Millisecond))

	var wg sync.WaitGroup
	for _, lane := range []string{"a", "b"} {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(l string) {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					_, _ = lanes.Write(l, textMessage("x"), overwrite)
				}
			}(lane)
		}
	}

	// Let some writes get in flight before pulling the rug out.
	time.Sleep(60 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		lanes.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Stop did not return while operations were in flight")
	}

	// The writers unwind cleanly once the readers are gone.
	wg.Wait()
}

// TestConcurrencySim_ReaderChurnDuringOperations: readers are added and dropped
// while operations run on the stable lanes. Reconciliation (opening and closing
// readers) must not race, deadlock, or disturb the lanes that stay put.
func TestConcurrencySim_ReaderChurnDuringOperations(t *testing.T) {
	stable := []string{"stable-1", "stable-2"}
	lanes := NewEmulatedLanes(t, stable...)
	for _, name := range stable {
		lanes.Present(name, NTAG215(uidFor(name)).WithText("seed"))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, name := range stable {
		wg.Add(1)
		go func(l string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = lanes.Write(l, textMessage("x"), overwrite)
			}
		}(name)
	}

	// Add and drop a transient reader repeatedly while the stable lanes work.
	for i := 0; i < 8; i++ {
		lanes.Plug("transient")
		lanes.Present("transient", NTAG215("04777777777777").WithText("t"))
		_, _ = lanes.Write("transient", textMessage("hi"), overwrite)
		lanes.Unplug("transient")
	}

	close(stop)
	wg.Wait()

	// The stable lanes still serve after all the reader churn.
	for _, name := range stable {
		final := NTAG215(uidFor(name + "-final")).WithText("seed")
		lanes.Remove(name, uidFor(name))
		lanes.Present(name, final)
		res, err := lanes.Write(name, textMessage("final"), overwrite)
		if err != nil {
			t.Errorf("stable lane %s stopped serving after reader churn: %v", name, err)
			continue
		}
		if !res.Verified {
			t.Errorf("stable lane %s final write should verify after churn, got %+v", name, res)
		}
	}
}
