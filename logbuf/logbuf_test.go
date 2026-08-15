package logbuf

import (
	"fmt"
	"log"
	"sync"
	"testing"
	"time"
)

func TestWriteSplitsLines(t *testing.T) {
	r := New(10)
	if _, err := r.Write([]byte("first\nsecond\nthird\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries := r.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	for i, want := range []string{"first", "second", "third"} {
		if entries[i].Message != want {
			t.Errorf("entry %d = %q, want %q", i, entries[i].Message, want)
		}
		if entries[i].Seq != uint64(i+1) {
			t.Errorf("entry %d seq = %d, want %d", i, entries[i].Seq, i+1)
		}
	}
}

// A logger is free to split a line across Writes. Recording the fragment as its
// own entry would corrupt both halves, so the remainder has to be held back.
func TestWriteHoldsPartialLine(t *testing.T) {
	r := New(10)
	_, _ = r.Write([]byte("incomplete"))

	if got := r.Len(); got != 0 {
		t.Fatalf("partial line recorded early: %d entries", got)
	}

	_, _ = r.Write([]byte(" line\n"))

	entries := r.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "incomplete line" {
		t.Errorf("message = %q, want %q", entries[0].Message, "incomplete line")
	}
}

func TestRingEvictsOldest(t *testing.T) {
	r := New(3)
	for i := 1; i <= 5; i++ {
		_, _ = fmt.Fprintf(r, "line %d\n", i)
	}

	entries := r.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (capacity)", len(entries))
	}
	for i, want := range []string{"line 3", "line 4", "line 5"} {
		if entries[i].Message != want {
			t.Errorf("entry %d = %q, want %q", i, entries[i].Message, want)
		}
	}
	// Seq keeps counting past eviction, so a tailing reader can tell that it
	// missed entries rather than silently resuming.
	if entries[0].Seq != 3 {
		t.Errorf("oldest surviving seq = %d, want 3", entries[0].Seq)
	}
}

func TestSinceReturnsOnlyNewer(t *testing.T) {
	r := New(10)
	for i := 1; i <= 5; i++ {
		_, _ = fmt.Fprintf(r, "line %d\n", i)
	}

	entries := r.Since(3)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Message != "line 4" || entries[1].Message != "line 5" {
		t.Errorf("got %q, %q; want line 4, line 5", entries[0].Message, entries[1].Message)
	}

	if got := r.Since(99); len(got) != 0 {
		t.Errorf("Since past the end returned %d entries, want 0", len(got))
	}
}

// The prefix and timestamp the standard logger prepends belong in their own
// fields, not smuggled into the message text.
func TestParsesStandardLoggerFormat(t *testing.T) {
	r := New(10)
	logger := log.New(r, "[agent] ", log.LstdFlags)
	logger.Print("Server started on port 9470")

	entries := r.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Source != "[agent]" {
		t.Errorf("source = %q, want %q", e.Source, "[agent]")
	}
	if e.Message != "Server started on port 9470" {
		t.Errorf("message = %q", e.Message)
	}
	if time.Since(e.Time) > time.Minute {
		t.Errorf("timestamp not parsed from the line: %v", e.Time)
	}
}

func TestUnparseableLineIsKeptWhole(t *testing.T) {
	r := New(10)
	_, _ = r.Write([]byte("a bare message with no stamp\n"))

	entries := r.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "a bare message with no stamp" {
		t.Errorf("message = %q", entries[0].Message)
	}
}

func TestInferLevel(t *testing.T) {
	cases := []struct {
		message string
		want    Level
	}{
		{"Server started on port 9470", LevelInfo},
		{"Warning: Auto-TLS failed", LevelError}, // "failed" outranks "warning"
		{"Warning: no devices are paired yet", LevelWarn},
		{"Error listing NFC devices", LevelError},
		{"origin rejected: evil.example.com", LevelError},
		{"retrying write after transient failure", LevelError},
	}
	for _, tc := range cases {
		if got := inferLevel(tc.message); got != tc.want {
			t.Errorf("inferLevel(%q) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

func TestSubscribeReceivesNewEntries(t *testing.T) {
	r := New(10)
	ch, cancel := r.Subscribe(4)
	defer cancel()

	_, _ = r.Write([]byte("hello\n"))

	select {
	case e := <-ch:
		if e.Message != "hello" {
			t.Errorf("message = %q, want %q", e.Message, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received nothing")
	}
}

// A subscriber that stops reading must not wedge the logger. The entry is still
// recoverable from the ring, so dropping it on the channel is the right trade.
func TestSlowSubscriberDoesNotBlockWriter(t *testing.T) {
	r := New(100)
	_, cancel := r.Subscribe(1)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = fmt.Fprintf(r, "line %d\n", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked on a subscriber that stopped reading")
	}

	if got := r.Len(); got != 50 {
		t.Errorf("ring holds %d entries, want 50", got)
	}
}

func TestCancelSubscriptionIsIdempotent(t *testing.T) {
	r := New(10)
	ch, cancel := r.Subscribe(4)

	cancel()
	cancel() // must not panic on a double close

	if _, open := <-ch; open {
		t.Error("channel still open after cancel")
	}

	_, _ = r.Write([]byte("after cancel\n")) // must not panic writing to a closed sub
}

func TestConcurrentWritesAndReads(t *testing.T) {
	r := New(500)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = fmt.Fprintf(r, "writer %d line %d\n", w, i)
			}
		}(w)
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r.Entries()
				r.Since(50)
				r.Len()
			}
		}()
	}
	wg.Wait()

	if got := r.Len(); got != 500 {
		t.Errorf("ring holds %d entries, want 500 (capacity)", got)
	}
}

func TestNewClampsCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		if got := New(capacity).capacity; got != DefaultCapacity {
			t.Errorf("New(%d) capacity = %d, want %d", capacity, got, DefaultCapacity)
		}
	}
}
