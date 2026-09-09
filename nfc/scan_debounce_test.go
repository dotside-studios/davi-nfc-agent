package nfc

import (
	"errors"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
)

var errScanTest = errors.New("scan debounce test error")

// tap and gone build the two NFCData shapes a reader emits: a card on the
// field, and a card that has left.
func tap(uid string) NFCData  { return NFCData{Card: &Card{UID: uid}} }
func gone(uid string) NFCData { return NFCData{RemovedUID: uid} }

func TestScanDebouncerSuppressesRepeatWithinWindow(t *testing.T) {
	d := NewScanDebouncer(3 * time.Second)
	now := time.Unix(0, 0)
	d.now = func() time.Time { return now }

	if !d.Allow(tap("AABB")) {
		t.Fatal("first presentation was suppressed")
	}
	now = now.Add(time.Second)
	if d.Allow(tap("AABB")) {
		t.Fatal("a repeat inside the window passed")
	}
	now = now.Add(3 * time.Second) // past the window from the first tap
	if !d.Allow(tap("AABB")) {
		t.Fatal("a presentation past the window was suppressed")
	}
}

func TestScanDebouncerRemovalClearsTheKey(t *testing.T) {
	d := NewScanDebouncer(3 * time.Second)
	now := time.Unix(0, 0)
	d.now = func() time.Time { return now }

	d.Allow(tap("AABB"))
	if !d.Allow(gone("AABB")) {
		t.Fatal("a removal was suppressed; it carries no card to debounce")
	}
	// A deliberate remove-and-re-present is a fresh presentation, even though it
	// is well inside the window.
	now = now.Add(time.Millisecond)
	if !d.Allow(tap("AABB")) {
		t.Fatal("a re-presentation after removal was debounced against the tap that left")
	}
}

func TestScanDebouncerKeysAreIndependent(t *testing.T) {
	d := NewScanDebouncer(3 * time.Second)
	now := time.Unix(0, 0)
	d.now = func() time.Time { return now }

	if !d.Allow(tap("AABB")) {
		t.Fatal("first card suppressed")
	}
	if !d.Allow(tap("CCDD")) {
		t.Fatal("a different card was debounced against the first")
	}
	if d.Allow(tap("AABB")) {
		t.Fatal("the first card passed a second time inside the window")
	}
}

func TestScanDebouncerPassesErrorsAndEmptyUIDs(t *testing.T) {
	d := NewScanDebouncer(time.Second)

	if !d.Allow(NFCData{Err: errScanTest}) {
		t.Error("an error carrying no card was suppressed")
	}
	// An empty UID is not a physical field to hold, so it is never debounced,
	// however many times it repeats.
	if !d.Allow(tap("")) {
		t.Error("an empty UID was suppressed")
	}
	if !d.Allow(tap("")) {
		t.Error("a repeated empty UID was debounced against the first")
	}
}

func TestScanDebouncerDefaultsAnEmptyWindow(t *testing.T) {
	if got := NewScanDebouncer(0).window; got != DefaultDebounceWindow {
		t.Fatalf("window = %v, want the default %v", got, DefaultDebounceWindow)
	}
	if got := NewScanDebouncer(-time.Second).window; got != DefaultDebounceWindow {
		t.Fatalf("negative window = %v, want the default %v", got, DefaultDebounceWindow)
	}
}

// The debouncer's whole point is to be a Subscribe filter: a held card's
// repeated polls collapse into one queued presentation.
func TestScanDebouncerComposesAsASubscribeFilter(t *testing.T) {
	var sig event.Signal[NFCData]
	d := NewScanDebouncer(3 * time.Second)
	now := time.Unix(0, 0)
	d.now = func() time.Time { return now }

	sub := sig.Subscribe(event.SubscribeOptions[NFCData]{Buffer: 8, Filter: d.Allow})
	defer sub.Close()

	sig.Emit(tap("AABB"))  // passes
	sig.Emit(tap("AABB"))  // suppressed: same card, same instant
	sig.Emit(tap("AABB"))  // suppressed
	sig.Emit(gone("AABB")) // passes (removal), clears the key
	now = now.Add(time.Millisecond)
	sig.Emit(tap("AABB")) // passes again: fresh after removal

	var got []NFCData
	for len(sub.C()) > 0 {
		got = append(got, <-sub.C())
	}
	if len(got) != 3 {
		t.Fatalf("queued %d events, want 3 (tap, removal, tap)", len(got))
	}
	if got[0].Card == nil || got[1].Card != nil || got[2].Card == nil {
		t.Fatalf("queued the wrong sequence: want tap, removal, tap")
	}
	if sub.Dropped() != 0 {
		t.Errorf("Dropped = %d, want 0: a suppressed scan is filtered, not dropped", sub.Dropped())
	}
}
