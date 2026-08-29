package pcsc

import (
	"testing"
	"time"
)

// awaitClosed reports whether ch is closed within the timeout.
func awaitClosed(ch <-chan struct{}, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func TestDeviceChangesStopsOnClose(t *testing.T) {
	m := NewManager()
	ch := m.DeviceChanges()

	m.Close()

	if !awaitClosed(ch, 2*time.Second) {
		t.Error("DeviceChanges channel was not closed by Close; the watch goroutine is still running")
	}
}

func TestDeviceChangesStopsEveryWatch(t *testing.T) {
	m := NewManager()
	channels := []<-chan struct{}{m.DeviceChanges(), m.DeviceChanges(), m.DeviceChanges()}

	m.Close()

	for i, ch := range channels {
		if !awaitClosed(ch, 2*time.Second) {
			t.Errorf("watch %d was not stopped by Close", i)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	m := NewManager()
	ch := m.DeviceChanges()

	m.Close()
	m.Close() // must not panic on a second close of the stop channel

	if !awaitClosed(ch, 2*time.Second) {
		t.Error("watch was not stopped")
	}
}

func TestZeroValueManagerCloses(t *testing.T) {
	// Close and DeviceChanges create the stop channel on first use, so a
	// Manager built as a literal behaves like one from NewManager.
	var m Manager
	ch := m.DeviceChanges()

	m.Close()

	if !awaitClosed(ch, 2*time.Second) {
		t.Error("watch on a zero-value Manager was not stopped")
	}
}

func TestDeviceChangesStopsWhenClosedBeforeWatching(t *testing.T) {
	m := NewManager()
	m.Close()

	// A watch started after Close must not outlive it.
	if !awaitClosed(m.DeviceChanges(), 2*time.Second) {
		t.Error("watch started after Close was not stopped")
	}
}

// Close must satisfy the bare-Close interface multimanager.MultiManager uses to
// fan shutdown out to its children. Without it, Close is never called.
var _ interface{ Close() } = (*Manager)(nil)
