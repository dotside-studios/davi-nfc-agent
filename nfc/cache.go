package nfc

import (
	"sync"
	"time"
)

// TagCache provides thread-safe caching of the last scanned NFC tag UID.
type TagCache struct {
	lastUID      string // Most recently scanned valid UID
	mu           sync.RWMutex
	lastSeenTime time.Time
}

// NewTagCache creates and initializes a new TagCache instance.
func NewTagCache() *TagCache {
	return &TagCache{
		lastSeenTime: time.Time{},
	}
}

// GetLastScanned returns the UID of the last successfully scanned tag.
func (c *TagCache) GetLastScanned() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastUID
}

// HasChanged checks if the given UID is new or different from the last scanned card.
// It returns true if this is a new card (different UID from last scan).
func (c *TagCache) HasChanged(uid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Always update lastSeenTime for any valid card detection
	c.lastSeenTime = time.Now()

	// If same card as last time, no change
	if uid == c.lastUID {
		return false
	}

	// Different card detected
	c.lastUID = uid
	return true
}

// IsCurrent reports whether uid is the card already published and still
// present, so a poll has nothing to read for it. It refreshes the last-seen
// time as HasChanged does.
func (c *TagCache) IsCurrent(uid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSeenTime = time.Now()
	return uid != "" && uid == c.lastUID
}

// Clear resets the cache to its initial state.
func (c *TagCache) Clear() {
	c.mu.Lock()
	c.lastUID = ""
	c.lastSeenTime = time.Time{}
	c.mu.Unlock()
}

// IsCardPresent checks if a card is still present based on the last seen time.
func (c *TagCache) IsCardPresent() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.lastSeenTime.IsZero() && time.Since(c.lastSeenTime) < time.Second
}

// UpdateLastSeenTime updates the global last seen time in the cache,
// indicating recent card activity. The uid parameter is currently
// not used for specific per-tag timestamping with the current cache structure
// but is retained from the original intended signature of ForceSeen.
func (tc *TagCache) UpdateLastSeenTime(uid string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.lastSeenTime = time.Now()
}
