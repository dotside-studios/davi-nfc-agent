package nfc

import (
	"context"
	"errors"
	"testing"
	"time"
)

// opReader builds a reader whose tag operations the test drives directly. The
// operation timeout is long enough that only the context ends a wait, unless a
// test says otherwise.
func opReader(t *testing.T, opTimeout time.Duration) *deviceReader {
	t.Helper()

	manager := NewMockManager()
	manager.MockDevice = NewMockDevice()

	reader, err := newDeviceReaderWithClock("mock:usb:001", manager, opTimeout, nil)
	if err != nil {
		t.Fatalf("newDeviceReaderWithClock: %v", err)
	}
	t.Cleanup(reader.Close)
	return reader
}

// awaitErr reports the error a call produced, or fails if it has not returned.
func awaitErr(t *testing.T, errs <-chan error, within time.Duration, what string) error {
	t.Helper()

	select {
	case err := <-errs:
		return err
	case <-time.After(within):
		t.Fatalf("%s did not return within %s", what, within)
		return nil
	}
}

// Cancelling the caller's context ends the wait rather than the operation: the
// call returns, and the operation is still running.
func TestWithTagOperationReturnsOnCancel(t *testing.T) {
	reader := opReader(t, time.Minute)

	release := make(chan struct{})
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- reader.withTagOperation(ctx, func() error {
			<-release
			return nil
		})
	}()

	// Let the operation start before the wait is given up on.
	waitForSlot(t, reader)
	cancel()

	err := awaitErr(t, errs, 2*time.Second, "withTagOperation")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("withTagOperation err = %v, want context.Canceled", err)
	}
	if got := reader.abandonedOperations(); got != 1 {
		t.Errorf("abandonedOperations = %d, want 1", got)
	}
}

// The abandoned operation keeps the reader until it finishes. Before this, the
// mutex was released by the abandoning side, so the next operation ran
// concurrently with one still driving the same tag.
func TestAbandonedOperationKeepsTheReader(t *testing.T) {
	reader := opReader(t, 150*time.Millisecond)

	release := make(chan struct{})
	first := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		first <- reader.withTagOperation(ctx, func() error {
			<-release
			return nil
		})
	}()
	waitForSlot(t, reader)
	cancel()

	if err := awaitErr(t, first, 2*time.Second, "the abandoned operation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first err = %v, want context.Canceled", err)
	}

	// The first operation is still running, so a second one must not start.
	var ran bool
	err := reader.withTagOperation(context.Background(), func() error {
		ran = true
		return nil
	})
	if ran {
		t.Error("a second operation ran while an abandoned one still held the reader")
	}
	if !errors.Is(err, ErrReaderBusy) {
		t.Errorf("second err = %v, want ErrReaderBusy", err)
	}

	// Once the first finishes, the reader takes operations again.
	close(release)
	if err := reader.withTagOperation(context.Background(), func() error { return nil }); err != nil {
		t.Errorf("the reader stayed busy after the abandoned operation finished: %v", err)
	}
}

// A busy refusal carries a code a client can act on, and says it may be
// repeated.
func TestBusyRefusalIsTypedAndRetryable(t *testing.T) {
	err := NewBusyError("acquire", ErrReaderBusy)

	var nfcErr *NFCError
	if !errors.As(err, &nfcErr) {
		t.Fatalf("NewBusyError produced %T, want *NFCError", err)
	}
	if nfcErr.Code != ErrCodeBusy {
		t.Errorf("code = %v, want ErrCodeBusy", nfcErr.Code)
	}
	if !errors.Is(err, ErrReaderBusy) {
		t.Error("the busy error does not match ErrReaderBusy")
	}
}

// The operation goroutine is tracked, so Stop waits for it instead of leaving
// it behind.
func TestStopWaitsForAnInFlightOperation(t *testing.T) {
	reader := opReader(t, 100*time.Millisecond)

	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		_ = reader.withTagOperation(context.Background(), func() error {
			close(started)
			<-release
			close(finished)
			return nil
		})
	}()
	<-started

	// Stop must not block for longer than its bound while the operation runs.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		reader.drainOperations()
	}()

	select {
	case <-stopped:
		t.Error("drainOperations returned while an operation was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-finished

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Error("drainOperations did not return after the operation finished")
	}
}

// A drain does not wait forever: an abandoned operation is stuck in a transfer
// that cannot be interrupted, and shutdown must not hang behind it.
func TestDrainIsBounded(t *testing.T) {
	reader := opReader(t, 50*time.Millisecond)

	release := make(chan struct{})
	defer close(release)

	started := make(chan struct{})
	go func() {
		_ = reader.withTagOperation(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	done := make(chan struct{})
	go func() {
		defer close(done)
		reader.drainOperations()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("drainOperations did not give up on an operation that never finishes")
	}
}

// waitForSlot waits until an operation has taken the reader's slot, so a test
// acts on a started operation rather than racing its goroutine.
func waitForSlot(t *testing.T, r *deviceReader) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for len(r.opSlot) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the operation never took the reader's slot")
		}
		time.Sleep(time.Millisecond)
	}
}
