package agent

import (
	"sort"
	"sync"
)

// cardTypeFilter decides which card types a scan may carry. An empty filter
// allows every type.
//
// It guards itself because it is read on the goroutine draining the reader and
// written by the console and the tray. A bare map here would not merely race
// under concurrent read and write, it would abort the process.
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

// replace swaps the whole filter, as an operator picking types does.
func (f *cardTypeFilter) replace(cardTypes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.allowed = make(map[string]bool, len(cardTypes))
	for _, cardType := range cardTypes {
		f.allowed[cardType] = true
	}
}

// list names what is allowed, sorted, or nil when nothing is filtered.
func (f *cardTypeFilter) list() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.allowed) == 0 {
		return nil
	}

	out := make([]string, 0, len(f.allowed))
	for cardType := range f.allowed {
		out = append(out, cardType)
	}
	sort.Strings(out)
	return out
}

func (f *cardTypeFilter) len() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.allowed)
}
