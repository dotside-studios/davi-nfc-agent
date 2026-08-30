package event

import (
	"sync"
	"sync/atomic"
)

// SubscribeOptions configures a [Signal.Subscribe].
type SubscribeOptions[T any] struct {
	// Buffer bounds the queue between the emitter and the reader. A value
	// below 1 is treated as 1. A scan or traffic consumer wants headroom for
	// the burst between an emission and the reader draining it; size it to the
	// burst, not to the run.
	Buffer int

	// Filter, when set, runs on the emitting goroutine before a value is
	// queued: a value it returns false for is neither queued nor counted as
	// dropped, because it was excluded on purpose rather than lost to
	// backpressure. Like a handler, it must not block. It is the seam a
	// stateful transform plugs into — see [github.com/dotside-studios/davi-nfc-agent/nfc.ScanDebouncer].
	Filter func(T) bool
}

// Subscription is a [Signal] drained through a channel, with the two numbers a
// channel alone cannot report: how many values a full buffer turned away, and
// how deep the queue is now. It is what [Signal.Channel] should have been for a
// consumer that must account for every value it did not get.
//
//	sub := agent.Events().Tag.Subscribe(event.SubscribeOptions[nfc.NFCData]{Buffer: 64})
//	defer sub.Close()
//	for data := range sub.C() {
//		handle(data)
//	}
//
// A full buffer drops rather than blocking, so a slow reader never stalls the
// emitter — but the drop is counted, not silent. [Subscription.Dropped] is the
// difference from [Signal.Channel], whose overflow no consumer can see.
//
// Every method is safe from any goroutine. Closing races an in-flight emission
// safely: a value already on its way is delivered or dropped, never sent to a
// closed channel.
type Subscription[T any] struct {
	ch      chan T
	conn    *Connection
	dropped atomic.Int64

	// mu guards closed and the channel's open/closed state. The offer path
	// holds it for reading while it sends, so a concurrent Close cannot close
	// the channel out from under a send in flight; Close holds it for writing.
	mu     sync.RWMutex
	closed bool
	once   sync.Once
}

// Subscribe drains the signal through a bounded channel, dropping and counting
// rather than blocking when the reader falls behind. The subscription follows
// the signal until [Subscription.Close].
//
// Unlike [Signal.Channel], the overflow is observable: a consumer that must not
// lose a value silently — a scan feed, a queue with a health readout — reads
// [Subscription.Dropped] and [Subscription.Depth] to know when it is falling
// behind. When every value truly matters, connect a handler instead and do the
// work there.
func (s *Signal[T]) Subscribe(opts SubscribeOptions[T]) *Subscription[T] {
	buffer := opts.Buffer
	if buffer < 1 {
		buffer = 1
	}
	sub := &Subscription[T]{ch: make(chan T, buffer)}
	filter := opts.Filter
	sub.conn = s.Connect(func(v T) {
		if filter != nil && !filter(v) {
			return
		}
		sub.offer(v)
	})
	return sub
}

// offer queues v, or counts a drop when the buffer is full. It runs on the
// emitting goroutine, so it never blocks: the send is non-blocking and the read
// lock it holds is only ever contended by Close.
func (s *Subscription[T]) offer(v T) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- v:
	default:
		s.dropped.Add(1)
	}
}

// C is the channel the values arrive on. It is closed by [Subscription.Close],
// so a range over it ends when the subscription does.
func (s *Subscription[T]) C() <-chan T { return s.ch }

// Dropped reports how many values a full buffer has turned away over the life
// of the subscription. A value the filter excluded is not counted here.
func (s *Subscription[T]) Dropped() int64 { return s.dropped.Load() }

// Depth reports how many values are queued and not yet drained.
func (s *Subscription[T]) Depth() int { return len(s.ch) }

// Close disconnects the handler and closes [Subscription.C]. Safe to call more
// than once and on a nil subscription, so it can be deferred without tracking
// whether something else got there first.
func (s *Subscription[T]) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.conn.Disconnect()
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
	})
}
