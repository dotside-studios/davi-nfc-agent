package event

// Property is a [Signal] that also reports the value it carries, so a
// subscriber draws its first frame from the same surface it follows:
//
//	conn := agent.Events().Preferences.Connect(render)
//
// Connect calls the handler with the current value before returning, which
// closes the gap between connecting and reading it separately. Value answers
// the same question without connecting.
//
// Use it for a signal carrying state, where a subscriber needs the value
// whether or not it has changed yet. A signal carrying traffic, such as a scan,
// stays a plain Signal: there is no current scan to replay.
//
// The zero value is a Signal with nothing to report. Every method is safe to
// call from any goroutine.
type Property[T any] struct {
	Signal[T]

	// Current reports the value the property carries. Assign it once, before
	// the property is published, and leave it alone afterwards: it is read on
	// every Connect and Value, from the caller's goroutine.
	//
	// It reads the state the emitter publishes rather than a copy held here, so
	// a replay cannot be older than the last emission. Nil replays nothing,
	// leaving a plain Signal.
	Current func() T
}

// Value is what the property carries now, or the zero value when it has no
// Current.
func (p *Property[T]) Value() T {
	if p.Current == nil {
		var zero T
		return zero
	}
	return p.Current()
}

// Connect registers fn and calls it with the current value before returning.
// Handlers connected this way run in connection order for every later emission,
// as on a Signal.
//
// [Signal.ConnectOnce] is inherited and does not replay: a handler that wants
// one emission wants the next one, not the value it could have read.
func (p *Property[T]) Connect(fn func(T)) *Connection {
	conn := p.Signal.Connect(fn)
	if fn != nil && p.Current != nil {
		fn(p.Current())
	}
	return conn
}

// Channel returns a channel carrying the current value followed by what the
// property emits, and the function that stops it. It drops on a full buffer,
// as [Signal.Channel] does.
func (p *Property[T]) Channel(buffer int) (<-chan T, func()) {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan T, buffer)

	conn := p.Connect(func(v T) {
		select {
		case ch <- v:
		default:
		}
	})
	return ch, conn.Disconnect
}
