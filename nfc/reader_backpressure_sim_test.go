package nfc

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestBackpressure_StalledConsumerSendsReleaseOnStop is the wedged-consumer worst
// case: a client that stops draining scans (a hung WebSocket, a slow browser)
// while cards keep arriving. Each scan the reader publishes goes to dataChan; once
// that buffer fills, further sends block inside the poll. Those blocked sends must
// be released when the reader stops — otherwise every one is a goroutine leaked
// for the life of the process, and a reader can never be torn down cleanly while a
// consumer is behind.
//
// The sends are driven directly here rather than through the worker, whose 5s
// poll watchdog would let only one blocked send accumulate at a time and hide the
// pile-up.
func TestBackpressure_StalledConsumerSendsReleaseOnStop(t *testing.T) {
	mgr := NewMockManager()
	dev := NewMockDevice()
	mgr.MockDevice = dev
	r, err := newDeviceReader("mock", mgr, 5*time.Second)
	if err != nil {
		t.Fatalf("newDeviceReader: %v", err)
	}
	// Nothing drains r.Data(): the consumer has stalled. dataChan holds one, then
	// every further scan send blocks.

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.handleTagPolling([]Tag{NewMockTag(fmt.Sprintf("04%012X", i))})
		}(i)
	}

	// Give the sends time to pile up on the blocked channel.
	time.Sleep(200 * time.Millisecond)

	// Stopping the reader must release every blocked send. Before the fix the
	// sends ignore stopChan and stay blocked forever, so wg.Wait never returns.
	r.Stop()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("scan sends stayed blocked after Stop — a stalled consumer leaks a goroutine per scan")
	}
}
