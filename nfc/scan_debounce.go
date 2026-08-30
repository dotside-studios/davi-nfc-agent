package nfc

import (
	"sync"
	"time"
)

// DefaultDebounceWindow is how long a held card's repeated scans are suppressed
// when [NewScanDebouncer] is given no window of its own. A reader that polls
// re-reports the same UID for as long as the card sits on the field; within
// this window those reads collapse into one.
const DefaultDebounceWindow = 3 * time.Second

// ScanDebouncer suppresses the repeated scans a polling reader emits while one
// card is held, so a consumer acts once per presentation rather than once per
// poll. It keys on the physical chip UID and clears that key when the tag
// leaves, so a deliberate remove-and-re-present is a fresh presentation rather
// than a suppressed duplicate.
//
// Its [ScanDebouncer.Allow] method has the shape of an [event.SubscribeOptions]
// filter, so it is dropped in front of the agent's scan signal without a
// wrapper:
//
//	d := nfc.NewScanDebouncer(nfc.DefaultDebounceWindow)
//	sub := agent.Events().Tag.Subscribe(event.SubscribeOptions[nfc.NFCData]{
//		Buffer: 64,
//		Filter: d.Allow,
//	})
//
// It debounces presentations only: a removal and an error carry no card to
// repeat, so both pass through, and a removal also clears the departed tag's
// key on the way. The zero value is not usable; construct one with
// [NewScanDebouncer].
type ScanDebouncer struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

// NewScanDebouncer returns a debouncer suppressing a repeated UID for window. A
// window of zero or less selects [DefaultDebounceWindow].
func NewScanDebouncer(window time.Duration) *ScanDebouncer {
	if window <= 0 {
		window = DefaultDebounceWindow
	}
	return &ScanDebouncer{
		window: window,
		now:    time.Now,
		last:   make(map[string]time.Time),
	}
}

// Allow reports whether data should pass, and records a presentation when it
// does. A card presented again within the window is suppressed; a card not seen
// recently passes and starts the window. A removal clears the departed tag's
// key and passes, so the next presentation of that card is not debounced
// against the one that just left; an error, carrying no card, passes untouched.
//
// It runs on the emitting goroutine when used as a filter, so it takes only a
// short lock and never blocks.
func (d *ScanDebouncer) Allow(data NFCData) bool {
	if data.Card == nil {
		// A removal or an error: nothing to debounce. Clear the departed tag so
		// its re-presentation reads fresh, then let it through.
		d.forget(data.RemovedUID)
		return true
	}
	return d.allow(data.Card.UID)
}

// allow reports whether a scan of uid should pass, recording it when it does.
// An empty UID is never debounced: it is not a physical field to hold.
func (d *ScanDebouncer) allow(uid string) bool {
	if uid == "" {
		return true
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if previous, ok := d.last[uid]; ok && now.Sub(previous) < d.window {
		return false
	}
	d.last[uid] = now
	return true
}

// forget drops a UID so a re-presentation after removal is not suppressed.
func (d *ScanDebouncer) forget(uid string) {
	if uid == "" {
		return
	}
	d.mu.Lock()
	delete(d.last, uid)
	d.mu.Unlock()
}
