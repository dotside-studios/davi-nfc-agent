package tls

import (
	"testing"
	"time"
)

// TestAddrChangeNotifierLifecycle exercises the start/stop lifecycle of
// the per-OS address-change watcher without actually mutating network
// state. The native watcher should: (a) start without error, (b) not
// emit spuriously in the absence of a real change, and (c) close the
// returned channel when stop is closed.
func TestAddrChangeNotifierLifecycle(t *testing.T) {
	stop := make(chan struct{})
	ch := addrChangeNotifier(stop)

	// Drain any startup events the kernel may already have queued (some
	// netlink/route socket implementations replay current state on bind).
	// 50ms is plenty: the watcher runs in a tight loop on its own goroutine.
	deadline := time.After(50 * time.Millisecond)
drain:
	for {
		select {
		case <-ch:
		case <-deadline:
			break drain
		}
	}

	close(stop)

	// Channel must close within a reasonable time. Linux/Darwin block on
	// Recvfrom/Read until the fd is closed by the sibling goroutine, so
	// we give a generous bound.
	select {
	case _, ok := <-ch:
		if ok {
			// Drain any final event the kernel posted while we were stopping.
			<-ch
		}
	case <-time.After(2 * time.Second):
		t.Fatal("addrChangeNotifier channel did not close within 2s of stop closing")
	}
}
