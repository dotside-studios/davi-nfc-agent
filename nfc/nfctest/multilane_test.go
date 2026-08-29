package nfctest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// These simulations model a deployment that is more than one reader: a floor of
// lanes driven by a single agent, each with its own card in its own field, an
// operation naming the lane it is for. The properties under test are the ones
// that only appear with several readers at once — that a write lands on the lane
// it named and no other, that an unqualified operation is refused rather than
// sent to an arbitrary lane, that lanes come and go without disturbing the rest,
// and that concurrent traffic across every lane stays free of races and
// deadlocks (run these under -race).

// TestMultiLane_IndependentFields: two lanes, each with its own card. A write to
// one lane must land on that lane's card and leave the other's untouched.
func TestMultiLane_IndependentFields(t *testing.T) {
	lanes := NewEmulatedLanes(t, "entry", "exit")

	entryCard := NTAG215("04E0E0E0E0E0E0").WithText("entry-seed")
	exitCard := NTAG215("04E1E1E1E1E1E1").WithText("exit-seed")
	lanes.Present("entry", entryCard)
	lanes.Present("exit", exitCard)

	res, err := lanes.Write("entry", textMessage("boarding-pass"), overwrite)
	if err != nil {
		t.Fatalf("write to entry lane: %v", err)
	}
	if !res.Verified {
		t.Errorf("entry write should verify, got %+v", res)
	}

	if !cardHolds(t, entryCard, "boarding-pass") {
		t.Error("entry card should hold the payload written to its lane")
	}
	if !cardHolds(t, exitCard, "exit-seed") {
		t.Error("exit card must be untouched by a write to the entry lane")
	}
}

// TestMultiLane_UnqualifiedWriteRefused: with more than one lane, a write that
// does not name a lane is ambiguous. It must be refused with a message that
// names the choices, never sent to whichever reader happens to be first.
func TestMultiLane_UnqualifiedWriteRefused(t *testing.T) {
	lanes := NewEmulatedLanes(t, "left", "right")
	lanes.Present("left", NTAG215("04AAAAAAAAAAAA").WithText("L"))
	lanes.Present("right", NTAG215("04BBBBBBBBBBBB").WithText("R"))

	_, err := lanes.Write("", textMessage("ambiguous"), overwrite)
	if err == nil {
		t.Fatal("expected an unqualified write across multiple lanes to be refused")
	}
	// The error should help the caller pick: it names the readers.
	if !strings.Contains(err.Error(), "left") || !strings.Contains(err.Error(), "right") {
		t.Errorf("ambiguity error should name the lanes to choose from, got: %v", err)
	}
}

// TestMultiLane_DuplicateUIDAcrossLanes: two cards sharing a UID (a cloned or
// batch-duplicated card) sit on two lanes at once. Routing is by lane, not by
// UID, so a write to one lane must reach that lane's card even though a card with
// the same UID sits on the other.
func TestMultiLane_DuplicateUIDAcrossLanes(t *testing.T) {
	lanes := NewEmulatedLanes(t, "gate-a", "gate-b")

	const clonedUID = "04C10NEDC10NED"
	a := NTAG215(clonedUID).WithText("on-gate-a")
	b := NTAG215(clonedUID).WithText("on-gate-b")
	lanes.Present("gate-a", a)
	lanes.Present("gate-b", b)

	if _, err := lanes.Write("gate-a", textMessage("stamped-a"), overwrite); err != nil {
		t.Fatalf("write to gate-a: %v", err)
	}

	if !cardHolds(t, a, "stamped-a") {
		t.Error("gate-a's card should carry the write routed to gate-a")
	}
	if !cardHolds(t, b, "on-gate-b") {
		t.Error("gate-b's same-UID card must be untouched by a write to gate-a")
	}
}

// TestMultiLane_OneLaneFailsOthersServe: a reader is unplugged mid-shift. Writes
// to it must fail cleanly while every other lane keeps serving.
func TestMultiLane_OneLaneFailsOthersServe(t *testing.T) {
	lanes := NewEmulatedLanes(t, "lane-1", "lane-2", "lane-3")
	for _, name := range []string{"lane-1", "lane-2", "lane-3"} {
		lanes.Present(name, NTAG215(uidFor(name)).WithText("seed"))
	}

	lanes.Unplug("lane-2")

	// The unplugged lane is gone: naming it is an error, not a silent misroute.
	if _, err := lanes.Write("lane-2", textMessage("nope"), overwrite); err == nil {
		t.Error("a write to an unplugged lane should fail")
	}

	// The survivors still serve.
	for _, name := range []string{"lane-1", "lane-3"} {
		res, err := lanes.Write(name, textMessage("served"), overwrite)
		if err != nil {
			t.Errorf("%s should still serve after lane-2 was unplugged: %v", name, err)
			continue
		}
		if !res.Verified {
			t.Errorf("%s write should verify, got %+v", name, res)
		}
	}
}

// TestMultiLane_HotplugMidShift: a lane added at runtime must become fully
// operable — present a card and write to it — without restarting the agent.
func TestMultiLane_HotplugMidShift(t *testing.T) {
	lanes := NewEmulatedLanes(t, "lane-1")
	lanes.Present("lane-1", NTAG215("04111111111111").WithText("seed"))

	lanes.Plug("lane-2")
	card := NTAG215("04222222222222").WithText("fresh")
	lanes.Present("lane-2", card)

	res, err := lanes.Write("lane-2", textMessage("hotplugged"), overwrite)
	if err != nil {
		t.Fatalf("write to a hot-plugged lane: %v", err)
	}
	if !res.Verified {
		t.Errorf("hot-plugged lane write should verify, got %+v", res)
	}
	if !cardHolds(t, card, "hotplugged") {
		t.Error("the hot-plugged lane's card should carry its write")
	}
}

// TestMultiLane_ConcurrentTrafficAllLanes hammers every lane at once with
// interleaved presents, removals, and writes. Individual writes may fail (no card
// at that instant); the point is that the supervisor's per-reader isolation holds
// up under concurrent cross-lane load without races, panics, or deadlocks
// (validated under -race), and that every lane still serves a final write.
func TestMultiLane_ConcurrentTrafficAllLanes(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	lanes := NewEmulatedLanes(t, names...)
	for _, name := range names {
		lanes.Present(name, NTAG215(uidFor(name)).WithText("seed"))
	}

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(lane string) {
			defer wg.Done()
			card := NTAG215(uidFor(lane + "-churn"))
			for i := 0; i < 30; i++ {
				lanes.Present(lane, card)
				_, _ = lanes.Write(lane, textMessage(fmt.Sprintf("%s-%d", lane, i)), overwrite)
				lanes.Remove(lane, card.UID())
			}
		}(name)
	}
	wg.Wait()

	// After the churn, clear each lane and re-seat a single known card, then
	// confirm it still takes a verified write: the reader survived the load.
	for _, name := range names {
		lanes.Remove(name, uidFor(name))          // the initial seed
		lanes.Remove(name, uidFor(name+"-churn")) // any churn card left on
		final := NTAG215(uidFor(name + "-final")).WithText("seed")
		lanes.Present(name, final)
		res, err := lanes.Write(name, textMessage("final"), overwrite)
		if err != nil {
			t.Errorf("lane %s failed its final write after churn: %v", name, err)
			continue
		}
		if !res.Verified {
			t.Errorf("lane %s final write should verify, got %+v", name, res)
		}
	}
}

// uidFor derives a stable 14-hex-digit UID from a lane name for tests that need
// distinct cards per lane without hand-writing UIDs.
func uidFor(name string) string {
	var b strings.Builder
	b.WriteString("04")
	for _, r := range name {
		fmt.Fprintf(&b, "%02X", byte(r))
	}
	for b.Len() < 14 {
		b.WriteByte('0')
	}
	return b.String()[:14]
}
