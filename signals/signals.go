// Package signals is a small, type-safe signal/slot primitive.
//
// A [Signal] is a fan-out point: any number of handlers connect to it, and
// every Emit calls them all. It is the inverse of a channel — a channel has one
// value and races its receivers for it, a signal has many receivers and gives
// each of them the value. That difference is what makes signals the right shape
// for UI events, where several unrelated parts of a program care about the same
// click and none of them should be able to consume it out from under another.
//
// The zero value is ready to use, and every method is safe to call from any
// goroutine, including from inside a handler:
//
//	var changed signals.Signal[string]
//
//	conn := changed.Connect(func(name string) { log.Println("now:", name) })
//	defer conn.Disconnect()
//
//	changed.Emit("device-2")
//
// Handlers run synchronously on the goroutine that calls Emit, in the order
// they connected. A handler that blocks blocks the emitter, so anything slow
// belongs in a goroutine of the handler's own.
package signals

import "sync"

// Signal delivers a value of type T to every connected handler.
//
// T is the whole payload: use a struct when an event carries several fields,
// and struct{} when the fact that it happened is the message.
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

// Connection is the handle to one connected handler. Disconnecting is the only
// thing a caller can do with it, and doing so twice is harmless — which lets a
// caller defer Disconnect without tracking whether something else got there
// first.
type Connection struct {
	once sync.Once
	off  func()
}

// Disconnect removes the handler. It is safe to call more than once, on a
// connection whose signal has been cleared, and on nil.
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
// connects nothing, so callers can pass an optional handler straight through.
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
// The handler list is snapshotted first and the lock released before any
// handler runs, so a handler is free to connect, disconnect or emit. A handler
// connected during an emission does not see that emission; one disconnected
// during it may still be called, because the decision was already taken.
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
