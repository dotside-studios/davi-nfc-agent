package virtualnfc

import (
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// memRoute is a Reader's in-process tag store: it carries the writes and locks
// that presented RoutedTags route to it, so a virtual tag is writable and
// lockable with no silicon behind it. Reads are served from each tag's scan-time
// snapshot (a RoutedTag's contract), so a write acknowledges but its new content
// is visible only when the tag is presented again.
type memRoute struct {
	mu      sync.Mutex
	content map[string][]byte
	locked  map[string]bool
}

func newMemRoute() *memRoute {
	return &memRoute{content: make(map[string][]byte), locked: make(map[string]bool)}
}

// seed records a tag's initial content and lock state when it is presented.
func (r *memRoute) seed(uid string, content []byte, readOnly bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.content[uid] = append([]byte(nil), content...)
	r.locked[uid] = readOnly
}

// forget drops a tag when it is removed from the field.
func (r *memRoute) forget(uid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.content, uid)
	delete(r.locked, uid)
}

// Content returns a copy of the current stored bytes for a tag.
func (r *memRoute) Content(uid string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.content[uid]...)
}

// Locked reports whether the tag has been locked.
func (r *memRoute) Locked(uid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.locked[uid]
}

// The bounds a virtual reader imposes on every tag it carries: it can write and
// lock, but does not model raw APDU exchange.
func (r *memRoute) CanWrite() bool      { return true }
func (r *memRoute) CanLock() bool       { return true }
func (r *memRoute) CanTransceive() bool { return false }

// Write stores the bytes for a tag, optionally locking it. A write to a locked
// tag is refused, as silicon would refuse it.
func (r *memRoute) Write(uid string, ndef []byte, lock bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locked[uid] {
		return fmt.Errorf("virtualnfc: tag %s is read-only", uid)
	}
	r.content[uid] = append([]byte(nil), ndef...)
	if lock {
		r.locked[uid] = true
	}
	return nil
}

// Lock makes a tag read-only.
func (r *memRoute) Lock(uid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locked[uid] = true
	return nil
}

// Transceive is not modeled by a virtual reader.
func (r *memRoute) Transceive(uid string, data []byte, raw bool) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("Transceive")
}

var _ Route = (*memRoute)(nil)
