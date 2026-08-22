package traymenu

import "sync"

// Signal is a fan-out point: any number of handlers connect to it, and every
// Emit calls them all. It is what a menu delivers its clicks on.
//
//	conn := item.Clicked().Connect(func(i *traymenu.Item) { log.Println(i.Title()) })
//	defer conn.Disconnect()
//
// The zero value is ready to use, and every method is safe to call from any
// goroutine, including from inside a handler. Handlers run synchronously on the
// goroutine that calls Emit, in the order they connected.
type Signal[T any] struct {
	mu     sync.Mutex
	nextID uint64
	slots  []slot[T]
}

type slot[T any] struct {
	id   uint64
	fn   func(T)
	once bool
}

// Connection is the handle to one connected handler.
type Connection struct {
	once sync.Once
	off  func()
}

// Disconnect removes the handler. It is safe to call more than once and on nil,
// so a caller can defer it without tracking whether something else got there
// first.
func (c *Connection) Disconnect() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		if c.off != nil {
			c.off()
		}
	})
}

// Connect registers fn and returns the handle that removes it again. A nil fn
// connects nothing, so an optional handler can be passed straight through.
func (s *Signal[T]) Connect(fn func(T)) *Connection { return s.connect(fn, false) }

// ConnectOnce registers fn for a single emission. The handler is removed before
// it is called, so it cannot see itself run twice even if it emits again.
func (s *Signal[T]) ConnectOnce(fn func(T)) *Connection { return s.connect(fn, true) }

func (s *Signal[T]) connect(fn func(T), once bool) *Connection {
	if fn == nil {
		return &Connection{}
	}

	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.slots = append(s.slots, slot[T]{id: id, fn: fn, once: once})
	s.mu.Unlock()

	return &Connection{off: func() { s.remove(id) }}
}

func (s *Signal[T]) remove(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, sl := range s.slots {
		if sl.id == id {
			s.slots = append(s.slots[:i], s.slots[i+1:]...)
			return
		}
	}
}

// Emit calls every connected handler with v, in connection order.
//
// The lock is released before any handler runs, so a handler may connect,
// disconnect or emit. One connected during an emission does not see that
// emission; one disconnected during it may still be called.
func (s *Signal[T]) Emit(v T) {
	s.mu.Lock()
	if len(s.slots) == 0 {
		s.mu.Unlock()
		return
	}

	fns := make([]func(T), 0, len(s.slots))
	keep := s.slots[:0]
	for _, sl := range s.slots {
		fns = append(fns, sl.fn)
		if !sl.once {
			keep = append(keep, sl)
		}
	}
	// Filtered in place: keep never overtakes the read cursor, so the entries
	// it overwrites have already been copied into fns.
	s.slots = keep
	s.mu.Unlock()

	for _, fn := range fns {
		fn(v)
	}
}

// Len reports how many handlers are connected.
func (s *Signal[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.slots)
}

// Clear disconnects every handler.
func (s *Signal[T]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slots = nil
}
