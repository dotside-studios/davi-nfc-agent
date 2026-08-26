package remotenfc

import (
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// stallScans holds the broadcast loop inside a subscriber until the returned
// function is called, so the queue behind it fills.
func stallScans(t *testing.T, m *Manager) func() {
	t.Helper()

	release := make(chan struct{})
	var released bool

	conn := m.Scans().Connect(func(nfc.ScannedTag) { <-release })
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		conn.Disconnect()
	})

	// The loop is only stalled once it is inside the subscriber.
	if err := m.publish(nfc.ScannedTag{Device: "primer"}); err != nil {
		t.Fatalf("prime the broadcast loop: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(m.dataChan) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	return func() {
		released = true
		close(release)
	}
}

// fillScanQueue publishes until the queue is full, failing the test if that
// never happens.
func fillScanQueue(t *testing.T, m *Manager) {
	t.Helper()

	for i := 0; i < ScanQueueDepth; i++ {
		if err := m.publish(nfc.ScannedTag{Device: "filler"}); err != nil {
			t.Fatalf("publish %d of %d: %v", i, ScanQueueDepth, err)
		}
	}
	if len(m.dataChan) != ScanQueueDepth {
		t.Fatalf("queue holds %d, want %d", len(m.dataChan), ScanQueueDepth)
	}
}

// A full queue used to be silent: the scan was discarded, SendTagData returned
// nil, and the device was told it had succeeded. A scan the agent cannot accept
// is now an error the device can see and retry.
func TestScanOverflowIsReported(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	m.publishTimeout = 50 * time.Millisecond

	_, deviceID := connectDevice(t, url)

	release := stallScans(t, m)
	defer release()
	fillScanQueue(t, m)

	err := m.SendTagData(deviceID, TagData{
		UID:        "04A1B2C3D4E5F6",
		Type:       "NTAG215",
		Technology: "ISO14443A",
	})
	if err == nil {
		t.Fatal("a scan the agent could not accept was reported as success")
	}
	if !strings.Contains(err.Error(), "queue full") {
		t.Errorf("error = %q, want it to name the full queue", err)
	}
	if got := m.Dropped(); got != 1 {
		t.Errorf("Dropped = %d, want 1", got)
	}

	// The device is told, with a code it can retry on.
	if payload := protocol.ErrorPayloadFor(err); payload.Code != protocol.ErrCodeUnknownError {
		t.Errorf("ErrorPayloadFor = %q, want an untyped error so sendTagError uses TAG_SEND_FAILED", payload.Code)
	}
	if !protocol.ErrCodeTagSendFailed.Retryable() {
		t.Error("TAG_SEND_FAILED is not retryable, so the device has no reason to send the scan again")
	}
}

// A removal that cannot be published is reported too. It matters as much as the
// scan: a client left holding a tag that has gone acts on a tag that is not
// there.
func TestRemovalOverflowIsReported(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	m.publishTimeout = 50 * time.Millisecond

	_, deviceID := connectDevice(t, url)

	release := stallScans(t, m)
	defer release()
	fillScanQueue(t, m)

	if err := m.SendTagRemoved(deviceID, TagRemovedData{UID: "04A1B2C3D4E5F6"}); err == nil {
		t.Fatal("a removal the agent could not accept was reported as success")
	}
	if got := m.Dropped(); got != 1 {
		t.Errorf("Dropped = %d, want 1", got)
	}
}

// A momentarily full queue is not a dropped scan: the publisher waits for room
// rather than discarding on the first refusal.
func TestScanWaitsForRoom(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	m.publishTimeout = 3 * time.Second

	_, deviceID := connectDevice(t, url)

	release := stallScans(t, m)
	fillScanQueue(t, m)

	done := make(chan error, 1)
	go func() {
		done <- m.SendTagData(deviceID, TagData{
			UID:        "04A1B2C3D4E5F6",
			Type:       "NTAG215",
			Technology: "ISO14443A",
		})
	}()

	// Still waiting, not yet failed.
	select {
	case err := <-done:
		t.Fatalf("publish returned %v before the queue drained", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publish after the queue drained: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publish never returned after the queue drained")
	}

	if got := m.Dropped(); got != 0 {
		t.Errorf("Dropped = %d, want 0: a wait is not a drop", got)
	}
}

// A closing manager releases a waiting publisher rather than holding it for the
// full timeout.
func TestPublishReleasedByClose(t *testing.T) {
	m := NewManager(time.Minute)
	m.publishTimeout = time.Minute

	release := stallScans(t, m)
	defer release()
	fillScanQueue(t, m)

	done := make(chan error, 1)
	go func() { done <- m.publish(nfc.ScannedTag{Device: "waiting"}) }()

	time.Sleep(20 * time.Millisecond)
	m.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("publish on a closed manager reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not release the waiting publisher")
	}
}
