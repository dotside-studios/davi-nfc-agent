package event_test

import (
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/event"
)

func TestSubscriptionCarriesEmittedValuesInOrder(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 4})
	defer sub.Close()

	sig.Emit(1)
	sig.Emit(2)

	if got := <-sub.C(); got != 1 {
		t.Errorf("first value is %d, want 1", got)
	}
	if got := <-sub.C(); got != 2 {
		t.Errorf("second value is %d, want 2", got)
	}
}

// The difference from Signal.Channel: the overflow is counted, not silent.
func TestSubscriptionFullBufferDropsAndCounts(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 1})
	defer sub.Close()

	for i := 0; i < 100; i++ {
		sig.Emit(i) // would deadlock if a full buffer blocked
	}

	if got := sub.Depth(); got != 1 {
		t.Errorf("Depth = %d, want 1 (the buffer is full)", got)
	}
	if got := sub.Dropped(); got != 99 {
		t.Errorf("Dropped = %d, want 99", got)
	}
	if got := <-sub.C(); got != 0 {
		t.Errorf("the buffered value is %d, want the first one emitted", got)
	}
	if got := sub.Depth(); got != 0 {
		t.Errorf("Depth = %d after draining, want 0", got)
	}
}

func TestSubscriptionZeroBufferIsUsable(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{}) // Buffer 0 → 1
	defer sub.Close()

	sig.Emit(7)
	if got := <-sub.C(); got != 7 {
		t.Errorf("value is %d, want 7", got)
	}
}

// A filtered-out value is excluded on purpose: not queued, and not counted as a
// backpressure drop.
func TestSubscriptionFilterExcludesWithoutCountingAsDropped(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{
		Buffer: 8,
		Filter: func(v int) bool { return v%2 == 0 },
	})
	defer sub.Close()

	for i := 0; i < 6; i++ {
		sig.Emit(i)
	}

	var got []int
	for len(sub.C()) > 0 {
		got = append(got, <-sub.C())
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 2 || got[2] != 4 {
		t.Fatalf("queued %v, want [0 2 4]", got)
	}
	if d := sub.Dropped(); d != 0 {
		t.Errorf("Dropped = %d, want 0: a filtered value is not a drop", d)
	}
}

func TestSubscriptionCloseEndsTheRange(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 4})
	sig.Emit(1)
	sig.Emit(2)
	sub.Close()

	var got []int
	for v := range sub.C() { // must terminate: Close closed the channel
		got = append(got, v)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("drained %v after Close, want the two buffered values [1 2]", got)
	}
}

func TestSubscriptionCloseStopsDelivery(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 4})
	sub.Close()
	sig.Emit(1) // must not panic on a closed channel, and must not be delivered

	if _, ok := <-sub.C(); ok {
		t.Fatal("a closed subscription delivered a value")
	}
	if sig.Len() != 0 {
		t.Fatalf("Len = %d after Close, want 0: the handler was not disconnected", sig.Len())
	}
}

func TestSubscriptionCloseIsIdempotentAndNilSafe(t *testing.T) {
	var sig event.Signal[int]

	sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 1})
	sub.Close()
	sub.Close() // must not panic (double close of the channel)

	var nilSub *event.Subscription[int]
	nilSub.Close() // must not panic
}

// Closing must not race an in-flight emission onto a closed channel. Run under
// -race to make the guarantee mean something.
func TestSubscriptionConcurrentEmitAndClose(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		var sig event.Signal[int]
		sub := sig.Subscribe(event.SubscribeOptions[int]{Buffer: 1})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				sig.Emit(i) // send onto a channel Close may be closing
			}
		}()

		sub.Close()
		wg.Wait()
	}
}
