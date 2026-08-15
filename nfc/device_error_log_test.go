package nfc

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

// captureLog collects everything written to the standard logger while fn runs.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	flags := log.Flags()
	output := log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(output)
		log.SetFlags(flags)
	})

	fn()
	return buf.String()
}

// closedChan is a stop channel that is already spent, so the retry following a
// report returns at once instead of waiting on a clock nothing advances.
func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func countLines(haystack, needle string) int {
	count := 0
	for _, line := range strings.Split(haystack, "\n") {
		if strings.Contains(line, needle) {
			count++
		}
	}
	return count
}

// A standing fault repeats on every poll, and the poll loop is continuous. The
// smartphone device closing produced hundreds of identical lines a second,
// which is the whole log.
func TestHandleError_LogsAPersistentFaultOnce(t *testing.T) {
	dm := NewDeviceManager(NewMockManager(), "mock:usb:001", NewFakeClock(time.Now()))
	stop := closedChan()
	err := fmt.Errorf("getTags: error from device.GetTags: %w", ErrDeviceClosed)

	out := captureLog(t, func() {
		for range 20 {
			dm.HandleError(err, stop)
		}
	})

	if got := countLines(out, "Device error:"); got != 1 {
		t.Errorf("expected the fault reported once across 20 polls, got %d lines:\n%s", got, out)
	}
}

// Reported once is not reported once ever: a different fault is news, and so is
// the first one returning after the device has worked.
func TestHandleError_ReportsAChangedFaultAndOneThatReturns(t *testing.T) {
	dm := NewDeviceManager(NewMockManager(), "mock:usb:001", NewFakeClock(time.Now()))
	stop := closedChan()
	closed := fmt.Errorf("getTags: %w", ErrDeviceClosed)

	out := captureLog(t, func() {
		dm.HandleError(closed, stop)
		dm.HandleError(closed, stop)
		dm.HandleError(fmt.Errorf("getTags: %w", ErrTimeout), stop)

		// A working device is what makes the first fault worth hearing about
		// again, and a poll that finds no card is the ordinary sign of one.
		dm.HandleError(&noCardError{ReaderName: "mock"}, stop)
		dm.HandleError(closed, stop)
	})

	if got := countLines(out, "Device error:"); got != 3 {
		t.Errorf("expected 3 reports (closed, changed, returned), got %d:\n%s", got, out)
	}
}

// A card not being on the reader is the ordinary state of one, so reporting it
// as a fault would report one continuously on a device that is working.
func TestHandleError_SaysNothingAboutAnAbsentCard(t *testing.T) {
	dm := NewDeviceManager(NewMockManager(), "mock:usb:001", NewFakeClock(time.Now()))

	out := captureLog(t, func() {
		dm.HandleError(&noCardError{ReaderName: "mock"}, closedChan())
	})

	if strings.Contains(out, "Device error:") {
		t.Errorf("expected no fault reported for an absent card, got:\n%s", out)
	}
}

// A reader that cannot be opened fails on every poll, and the poll loop is
// continuous. A phone pinned as the reader never becomes openable at all, which
// is how one wrong setting became the whole log.
func TestPoll_ReportsAnUnopenableReaderOnce(t *testing.T) {
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	manager.OpenDeviceError = errors.New("device not found: mock:usb:001")

	var reader *NFCReader
	out := captureLog(t, func() {
		var err error
		reader, err = NewNFCReaderWithClock("mock:usb:001", manager, 5*time.Second, NewFakeClock(time.Now()))
		if err != nil {
			t.Fatalf("NewNFCReaderWithClock: %v", err)
		}
		for range 20 {
			reader.doPoll()
		}
	})
	defer reader.Close()

	if got := countLines(out, "Connection attempt failed"); got > 1 {
		t.Errorf("expected at most one report across 20 polls, got %d:\n%s", got, out)
	}
}

// Once is once per reason, not once per run: a reader that comes back and fails
// again later is worth hearing about both times.
func TestPoll_ReportsAgainAfterTheReaderWorked(t *testing.T) {
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	manager.MockDevice = NewMockDevice()
	manager.OpenDeviceError = errors.New("device not found: mock:usb:001")

	reader, err := NewNFCReaderWithClock("mock:usb:001", manager, 5*time.Second, NewFakeClock(time.Now()))
	if err != nil {
		t.Fatalf("NewNFCReaderWithClock: %v", err)
	}
	defer reader.Close()

	// Construction already tried to connect, and reported that. This test is
	// about what the polls after it say.
	reader.deviceManager.clearLastError()

	out := captureLog(t, func() {
		reader.doPoll()
		reader.doPoll()

		manager.OpenDeviceError = nil
		reader.doPoll()

		reader.deviceManager.Close()
		manager.OpenDeviceError = errors.New("device not found: mock:usb:001")
		reader.doPoll()
	})

	if got := countLines(out, "Connection attempt failed"); got != 2 {
		t.Errorf("expected the fault reported on each side of a working reader, got %d:\n%s", got, out)
	}
}
