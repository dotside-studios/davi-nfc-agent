// Package logbuf keeps the agent's most recent log output in memory so it can
// be read back after the fact, there being no stderr to read when the agent is
// started from a desktop launcher.
package logbuf

import (
	"io"
	"strings"
	"sync"
	"time"
)

// Level is the severity a log line was recorded at. A line arrives at
// [LevelInfo] unless it was written to a writer from [Ring.At], so what a line
// is depends on where its logger writes, not on what its text happens to say.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Entry is one captured log line.
type Entry struct {
	// Seq is monotonic in write order, so a reader can ask for only what
	// followed the last entry it saw.
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   Level     `json:"level"`
	Source  string    `json:"source,omitempty"` // logger prefix, e.g. "[agent]"
	Message string    `json:"message"`
}

// DefaultCapacity is the number of entries kept when none is specified.
const DefaultCapacity = 5000

// Ring is a bounded, concurrency-safe buffer of the most recent log entries.
// It implements io.Writer and fans each entry out to subscribers.
type Ring struct {
	mu       sync.RWMutex
	entries  []Entry // len == capacity once full; used as a circular buffer
	capacity int
	next     int    // write cursor into entries
	count    int    // entries held, saturating at capacity
	seq      uint64 // total entries ever written; also the next Seq to assign

	subs map[int]chan Entry
	subN int

	// partial holds bytes from a Write that did not end at a line boundary.
	partial []byte
}

// New returns a Ring holding up to capacity entries. Non-positive capacity
// falls back to DefaultCapacity.
func New(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{
		entries:  make([]Entry, capacity),
		capacity: capacity,
		subs:     make(map[int]chan Entry),
	}
}

// Write implements io.Writer, recording each complete line as an entry. It
// never reports an error, which would propagate into the code trying to log.
func (r *Ring) Write(p []byte) (int, error) {
	return r.write(p, LevelInfo, &r.partial)
}

// At returns a writer recording everything written to it at level, for a
// logger whose lines are all one severity:
//
//	errors := log.New(ring.At(logbuf.LevelError), "[backups] ", log.LstdFlags)
//
// Each writer buffers its own partial line, so one that ends mid-line does not
// take the level of whatever writes next.
func (r *Ring) At(level Level) io.Writer {
	return &levelWriter{ring: r, level: level}
}

// levelWriter records at one level. Its own partial buffer is what keeps the
// levels from bleeding into each other.
type levelWriter struct {
	ring  *Ring
	level Level

	mu      sync.Mutex
	partial []byte
}

func (w *levelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ring.write(p, w.level, &w.partial)
}

func (r *Ring) write(p []byte, level Level, partial *[]byte) (int, error) {
	n := len(p)

	r.mu.Lock()
	*partial = append(*partial, p...)
	buf := *partial

	var complete [][]byte
	for {
		idx := indexByte(buf, '\n')
		if idx < 0 {
			break
		}
		complete = append(complete, buf[:idx])
		buf = buf[idx+1:]
	}
	// Copied because buf aliases the partial buffer's array.
	*partial = append([]byte(nil), buf...)

	added := make([]Entry, 0, len(complete))
	for _, line := range complete {
		text := strings.TrimRight(string(line), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		added = append(added, r.appendLocked(text, level))
	}

	subs := make([]chan Entry, 0, len(r.subs))
	for _, ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	// Fanned out with the lock released so a slow subscriber cannot stall the
	// logger.
	for _, e := range added {
		for _, ch := range subs {
			select {
			case ch <- e:
			default:
				// Behind. The entry is still in the ring, recoverable via Since.
			}
		}
	}

	return n, nil
}

// appendLocked records one line at level. Caller holds the write lock.
func (r *Ring) appendLocked(text string, level Level) Entry {
	ts, source, message := parseLine(text)

	r.seq++
	e := Entry{
		Seq:     r.seq,
		Time:    ts,
		Level:   level,
		Source:  source,
		Message: message,
	}

	r.entries[r.next] = e
	r.next = (r.next + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
	return e
}

// Entries returns the buffered entries, oldest first.
func (r *Ring) Entries() []Entry {
	return r.Since(0)
}

// Since returns the buffered entries with Seq greater than seq, oldest first.
// A caller whose seq predates the ring receives only what survives.
func (r *Ring) Since(seq uint64) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Entry, 0, r.count)
	start := 0
	if r.count == r.capacity {
		start = r.next
	}
	for i := 0; i < r.count; i++ {
		e := r.entries[(start+i)%r.capacity]
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// Len returns the number of entries currently held.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Subscribe returns a channel of entries recorded from now on and a function
// that closes it. Entries arriving while the buffer is full are dropped; track
// Seq and backfill with Since if none may be missed.
func (r *Ring) Subscribe(buffer int) (<-chan Entry, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	ch := make(chan Entry, buffer)

	r.mu.Lock()
	id := r.subN
	r.subN++
	r.subs[id] = ch
	r.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, id)
			r.mu.Unlock()
			close(ch)
		})
	}
}

// parseLine splits "[prefix ]2006/01/02 15:04:05 message" into its parts.
// Anything that does not match is kept whole with the current time.
func parseLine(line string) (time.Time, string, string) {
	now := time.Now()
	rest := line

	var source string
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end > 0 {
			source = rest[:end+1]
			rest = strings.TrimLeft(rest[end+1:], " ")
		}
	}

	// A trailing space must follow for this to be a stamp, not the message.
	const stampLen = 19
	if len(rest) > stampLen && rest[stampLen] == ' ' {
		if ts, err := time.ParseInLocation("2006/01/02 15:04:05", rest[:stampLen], time.Local); err == nil {
			return ts, source, strings.TrimLeft(rest[stampLen+1:], " ")
		}
	}

	return now, source, rest
}

// indexByte is strings.IndexByte without the conversion.
func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
