package agent

import "sync"

// cardTypeFilter decides which card types a scan may carry. An empty filter
// allows every type.
//
// It guards itself because it is read on the goroutine draining the reader and
// written by the console and the tray. It used to be a bare map shared between
// them, which raced, and a map is not merely racy under concurrent read and
// write but liable to abort the process.
type cardTypeFilter struct {
	mu      sync.RWMutex
	allowed map[string]bool
}

func newCardTypeFilter() *cardTypeFilter {
	return &cardTypeFilter{allowed: make(map[string]bool)}
}

func (f *cardTypeFilter) allow(cardType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed[cardType] = true
}

func (f *cardTypeFilter) disallow(cardType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.allowed, cardType)
}

// allowAll names every known type, which is what the tray's "allow all" does.
// It is not the same as an empty filter, though both admit every scan: the
// tray reads the names back to tick its menu.
func (f *cardTypeFilter) allowAll(types []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cardType := range types {
		f.allowed[cardType] = true
	}
}

// clear empties the filter. An empty filter admits every type.
func (f *cardTypeFilter) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed = make(map[string]bool)
}

func (f *cardTypeFilter) isAllowed(cardType string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if len(f.allowed) == 0 {
		return true
	}
	return f.allowed[cardType]
}

// explicitlyAllowed reports whether cardType was named, rather than admitted by
// the filter being empty. This is what the tray's menu ticks read.
func (f *cardTypeFilter) explicitlyAllowed(cardType string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.allowed[cardType]
}

func (f *cardTypeFilter) len() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.allowed)
}
