package nfctest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// These simulations push bursty and uneven throughput at the agent — the load
// shapes a steady tap-per-second test never sees: a crowd tapping a lane at once,
// a card bounced on and off, many cards dropped on one reader together, a floor
// where one lane is slammed while another sits idle, and every lane bursting on
// the same beat. The properties under test are that the agent converges to a
// correct steady state after a burst, never wedges or corrupts a tag under it,
// and keeps a quiet lane responsive while a busy one is saturated. Run under
// -race.

// TestThroughput_SingleLaneWriteBurst fires a burst of concurrent writes at one
// reader. The writes serialize through the reader's single operation slot; some
// may be refused while the slot is held, but the reader must never corrupt the
// tag or wedge, and a settling write afterwards must land exactly.
func TestThroughput_SingleLaneWriteBurst(t *testing.T) {
	card := NTAG215("04B0B0B0B0B0B0").WithText("seed")
	reader := NewEmulatedReader(t, card)

	var wg sync.WaitGroup
	for w := 0; w < 24; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				_, _ = reader.WriteMessage(textMessage("burst"), overwrite)
			}
		}(w)
	}
	wg.Wait()

	// After the storm, a settling write must land and read back exactly — the
	// reader is consistent, not left in a torn state.
	res, err := reader.WriteMessage(textMessage("settled"), overwrite)
	if err != nil {
		t.Fatalf("settling write after a burst failed: %v", err)
	}
	if !res.Verified {
		t.Errorf("settling write should verify, got %+v", res)
	}
	if !cardHolds(t, card, "settled") {
		t.Error("card should hold exactly the settling value after a write burst")
	}
}

// TestThroughput_CardBounceIsDebouncedThenReemits pins down how the reader
// treats a card that flickers on the field versus one genuinely re-tapped. A
// bounce faster than the card-presence window (a jittery field, a hand that
// wobbles) is one logical tap and must not re-emit; a re-tap after the card has
// been gone long enough for its absence to register is a fresh tap and must emit
// again. Getting this wrong means either duplicate scans on every jitter or a
// missed second tap.
func TestThroughput_CardBounceIsDebouncedThenReemits(t *testing.T) {
	const uid = "04B0FFFFFFFFFF"

	countScans := func(gap time.Duration) int {
		reader := NewEmulatedReader(t)
		var mu sync.Mutex
		scans := 0
		conn := reader.Scans().Connect(func(d nfc.NFCData) {
			if d.Card != nil && d.Card.UID == uid {
				mu.Lock()
				scans++
				mu.Unlock()
			}
		})
		defer conn.Disconnect()

		reader.Present(NTAG215(uid).WithText("member"))
		waitFor(t, 2*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return scans >= 1
		}, "the first tap to be scanned")
		reader.Remove(uid)
		time.Sleep(gap)
		reader.Present(NTAG215(uid).WithText("member"))
		time.Sleep(700 * time.Millisecond) // room for a second scan to land

		mu.Lock()
		defer mu.Unlock()
		return scans
	}

	// A fast bounce (well inside the ~1s presence window) is a single tap.
	if got := countScans(300 * time.Millisecond); got != 1 {
		t.Errorf("a fast bounce was scanned %d times; it should debounce to a single tap", got)
	}
	// A re-tap after the card has been absent long enough is a fresh tap.
	if got := countScans(1500 * time.Millisecond); got != 2 {
		t.Errorf("a genuine re-tap was scanned %d times; a returning card must re-emit", got)
	}
}

// TestThroughput_ManyCardsDroppedThenSettle: a handful of cards land on one
// reader at once (a stack of cards, a wallet). An operation is ambiguous and must
// be refused rather than pick one or crash; once the field is cleared to a single
// card the reader serves it.
func TestThroughput_ManyCardsDroppedThenSettle(t *testing.T) {
	reader := NewEmulatedReader(t)

	cards := []*EmulatedCard{
		NTAG215("04D000000000A1").WithText("1"),
		NTAG215("04D000000000A2").WithText("2"),
		NTAG215("04D000000000A3").WithText("3"),
		NTAG215("04D000000000A4").WithText("4"),
	}
	reader.Present(cards...)

	if _, err := reader.WriteMessage(textMessage("ambiguous"), overwrite); err == nil {
		t.Error("a write with several cards in the field should be refused")
	}

	// Clear down to a single card; the reader must then serve it.
	for _, c := range cards[1:] {
		reader.Remove(c.UID())
	}
	waitForWrite(t, reader, "survivor")
}

// TestThroughput_SkewedLoadKeepsIdleLaneResponsive: one lane is saturated with a
// sustained write burst while another sits idle. Writes to the idle lane must
// still complete with bounded latency — a busy lane must not starve a quiet one.
func TestThroughput_SkewedLoadKeepsIdleLaneResponsive(t *testing.T) {
	lanes := NewEmulatedLanes(t, "hammered", "idle")
	lanes.Present("hammered", NTAG215(uidFor("hammered")).WithText("seed"))
	lanes.Present("idle", NTAG215(uidFor("idle")).WithText("seed"))

	stop := make(chan struct{})
	var burst sync.WaitGroup
	for w := 0; w < 6; w++ {
		burst.Add(1)
		go func() {
			defer burst.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = lanes.Write("hammered", textMessage("x"), overwrite)
			}
		}()
	}
	defer func() { close(stop); burst.Wait() }()

	// Give the burst a moment to saturate the hammered lane.
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 5; i++ {
		start := time.Now()
		res, err := lanes.Write("idle", textMessage("quick"), overwrite)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("idle-lane write %d failed under skewed load: %v", i, err)
		}
		if !res.Verified {
			t.Errorf("idle-lane write %d should verify, got %+v", i, res)
		}
		if elapsed > 2*time.Second {
			t.Errorf("idle-lane write %d took %v while another lane was saturated — not isolated", i, elapsed)
		}
	}
}

// TestThroughput_ThunderingHerdAllLanes releases a synchronized write burst on
// every lane at once. Every lane must converge and still serve a final write —
// the aggregate handles a floor-wide surge, not just staggered traffic.
func TestThroughput_ThunderingHerdAllLanes(t *testing.T) {
	names := []string{"g1", "g2", "g3", "g4", "g5"}
	lanes := NewEmulatedLanes(t, names...)
	for _, name := range names {
		lanes.Present(name, NTAG215(uidFor(name)).WithText("seed"))
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, name := range names {
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(lane string) {
				defer wg.Done()
				<-start // all lanes fire on the same beat
				for i := 0; i < 12; i++ {
					_, _ = lanes.Write(lane, textMessage("herd"), overwrite)
				}
			}(name)
		}
	}
	close(start)
	wg.Wait()

	for _, name := range names {
		res, err := lanes.Write(name, textMessage("after-herd"), overwrite)
		if err != nil {
			t.Errorf("lane %s failed its settling write after the herd: %v", name, err)
			continue
		}
		if !res.Verified {
			t.Errorf("lane %s settling write should verify, got %+v", name, res)
		}
	}
}

// TestThroughput_SlowScanConsumerDoesNotStallWrites connects a deliberately slow
// scan consumer and confirms the write path is unaffected: scans and operations
// are separate paths, so a client that reads scans slowly must not slow the
// writes a reader performs.
func TestThroughput_SlowScanConsumerDoesNotStallWrites(t *testing.T) {
	card := NTAG215("045105105105AA").WithText("seed")
	reader := NewEmulatedReader(t, card)

	// A consumer that takes its time on every scan.
	conn := reader.Scans().Connect(func(nfc.NFCData) {
		time.Sleep(100 * time.Millisecond)
	})
	defer conn.Disconnect()

	// Provoke scan traffic alongside the writes.
	go func() {
		for i := 0; i < 20; i++ {
			reader.Present(NTAG215("045105105105BB").WithText("passing"))
			reader.Remove("045105105105BB")
		}
	}()

	for i := 0; i < 5; i++ {
		start := time.Now()
		res, err := reader.WriteMessage(textMessage("write-through"), overwrite)
		elapsed := time.Since(start)
		if err != nil {
			// A transient "no card" during the churn is acceptable; a stall is not.
			if elapsed > 2*time.Second {
				t.Fatalf("write %d stalled behind a slow scan consumer: %v after %v", i, err, elapsed)
			}
			continue
		}
		if elapsed > 2*time.Second {
			t.Errorf("write %d took %v behind a slow scan consumer — the paths are not independent", i, elapsed)
		}
		_ = res
	}
}

// waitFor polls cond until it holds or the timeout elapses, failing the test on
// timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitForWrite retries a write until it lands or a short deadline passes, for the
// case where the reader needs a poll cycle to settle onto the presented card.
func waitForWrite(t *testing.T, reader *EmulatedReader, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		res, err := reader.Supervisor.WriteMessage(ctx, "", textMessage(payload), overwrite)
		if err == nil && res.Verified {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("reader did not settle onto the single remaining card: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
