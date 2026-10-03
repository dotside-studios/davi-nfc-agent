package ntag424

import (
	"errors"
	"fmt"
	"sync"
)

// ErrReplay reports a URL whose MAC verified but whose counter does not advance
// past the last one seen for its tag.
var ErrReplay = errors.New("ntag424: counter does not advance, the URL was replayed")

// CounterStore remembers the highest read counter seen per tag, which is what
// tells a fresh tap from a captured URL.
type CounterStore interface {
	// Seen records counter for uid and reports whether it is higher than any
	// before. A counter that is not fresh must not be recorded.
	Seen(uid string, counter uint32) (fresh bool, err error)
}

// MemoryCounterStore is a CounterStore held in memory, lost on restart. The
// zero value is ready to use.
type MemoryCounterStore struct {
	mu   sync.Mutex
	last map[string]uint32
}

// Seen implements CounterStore.
func (m *MemoryCounterStore) Seen(uid string, counter uint32) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.last[uid]; ok && counter <= last {
		return false, nil
	}
	if m.last == nil {
		m.last = make(map[string]uint32)
	}
	m.last[uid] = counter
	return true, nil
}

// VerifyURLFresh is VerifyURL that also refuses a replay: the tap is returned
// only when its counter is higher than the last stored for its UID. The store
// is consulted after the MAC verifies, so an unverified URL never advances it.
func VerifyURLFresh(rawURL string, keys Keys, store CounterStore) (*Tap, error) {
	if store == nil {
		return nil, fmt.Errorf("ntag424: VerifyURLFresh needs a counter store")
	}
	tap, err := VerifyURL(rawURL, keys)
	if err != nil {
		return nil, err
	}
	fresh, err := store.Seen(tap.UIDString(), tap.ReadCounter)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return nil, ErrReplay
	}
	return tap, nil
}
