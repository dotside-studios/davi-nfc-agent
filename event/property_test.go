package event

import (
	"slices"
	"testing"
)

// A subscriber connecting to a property draws its first frame from the same
// surface it follows. Reading the value separately leaves a gap where an
// emission between the read and the connect is missed.
func TestConnectReplaysTheCurrentValue(t *testing.T) {
	value := 7
	p := Property[int]{Current: func() int { return value }}

	var seen []int
	p.Connect(func(v int) { seen = append(seen, v) })

	value = 9
	p.Emit(value)

	want := []int{7, 9}
	if !slices.Equal(seen, want) {
		t.Errorf("handler saw %v, want %v", seen, want)
	}
}

// A property with nothing to report is a plain signal, so a zero value is
// usable and a caller that has not assigned Current yet is not called with a
// zero it would render.
func TestConnectWithoutCurrentReplaysNothing(t *testing.T) {
	var p Property[int]

	var seen []int
	p.Connect(func(v int) { seen = append(seen, v) })

	p.Emit(4)

	if !slices.Equal(seen, []int{4}) {
		t.Errorf("handler saw %v, want [4]", seen)
	}
	if got := p.Value(); got != 0 {
		t.Errorf("Value() = %d without a Current, want 0", got)
	}
}

// The embedded signal is the way out of the replay, for a subscriber that wants
// the next value rather than this one. The tray connects to State that way, so
// its menu keeps saying "Starting..." until the agent settles.
func TestSignalConnectDoesNotReplay(t *testing.T) {
	p := Property[int]{Current: func() int { return 7 }}

	var seen []int
	p.Signal.Connect(func(v int) { seen = append(seen, v) })

	p.Emit(9)

	if !slices.Equal(seen, []int{9}) {
		t.Errorf("handler saw %v, want [9]", seen)
	}
}

// ConnectOnce is inherited and takes the next emission: a handler wanting one
// value could have read it.
func TestConnectOnceTakesTheNextEmission(t *testing.T) {
	p := Property[int]{Current: func() int { return 7 }}

	var seen []int
	p.ConnectOnce(func(v int) { seen = append(seen, v) })

	p.Emit(9)
	p.Emit(11)

	if !slices.Equal(seen, []int{9}) {
		t.Errorf("handler saw %v, want [9]", seen)
	}
}

func TestChannelCarriesTheCurrentValueFirst(t *testing.T) {
	p := Property[int]{Current: func() int { return 7 }}

	ch, stop := p.Channel(4)
	defer stop()

	p.Emit(9)

	var seen []int
	for len(seen) < 2 {
		seen = append(seen, <-ch)
	}
	if !slices.Equal(seen, []int{7, 9}) {
		t.Errorf("channel carried %v, want [7 9]", seen)
	}
}

func TestDisconnectStopsAPropertyHandler(t *testing.T) {
	p := Property[int]{Current: func() int { return 7 }}

	calls := 0
	conn := p.Connect(func(int) { calls++ })
	if calls != 1 {
		t.Fatalf("the replay ran %d times, want 1", calls)
	}

	conn.Disconnect()
	p.Emit(9)

	if calls != 1 {
		t.Errorf("a disconnected handler ran, calls = %d", calls)
	}
}

// A nil handler connects nothing, as on a Signal, so an optional subscriber can
// be passed straight through without a replay panicking on it.
func TestConnectNilHandler(t *testing.T) {
	p := Property[int]{Current: func() int { panic("Current must not run for a nil handler") }}

	p.Connect(nil)
	p.Emit(9)

	if p.Len() != 0 {
		t.Errorf("%d handlers connected, want 0", p.Len())
	}
}
