package nfc

import (
	"errors"
	"testing"
	"time"
)

// newFeedbackTestReader builds a reader on a mock device presenting one tag,
// and returns both so a test can assert on what the device was told.
func newFeedbackTestReader(t *testing.T, tag Tag) (*deviceReader, *MockDevice) {
	t.Helper()

	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	device := NewMockDevice()
	device.SetTags([]Tag{tag})
	manager.MockDevice = device

	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("newDeviceReader() failed: %v", err)
	}
	t.Cleanup(reader.Close)

	time.Sleep(100 * time.Millisecond)
	return reader, device
}

// waitForSignals waits for the reader's background signal to land.
func waitForSignals(t *testing.T, device *MockDevice, want int) []Signal {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := device.GetSignals(); len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			return device.GetSignals()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSignalString(t *testing.T) {
	tests := []struct {
		signal Signal
		want   string
	}{
		{SignalSuccess, "success"},
		{SignalFailure, "failure"},
		{Signal(42), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.signal.String(); got != tt.want {
			t.Errorf("Signal(%d).String() = %q, want %q", tt.signal, got, tt.want)
		}
	}
}

func TestFeedbackOffByDefault(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true

	reader, device := newFeedbackTestReader(t, tag)
	if reader.FeedbackEnabled() {
		t.Error("FeedbackEnabled() = true, want false before it is turned on")
	}

	if _, err := reader.WriteMessageWithResult(textMessage("quiet"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	}); err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}

	if got := waitForSignals(t, device, 1); len(got) != 0 {
		t.Errorf("reader signalled %v with feedback off", got)
	}
}

func TestFeedbackOnWrite(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true

	reader, device := newFeedbackTestReader(t, tag)
	reader.SetFeedback(true)

	if !reader.FeedbackEnabled() {
		t.Error("FeedbackEnabled() = false after SetFeedback(true)")
	}

	if _, err := reader.WriteMessageWithResult(textMessage("hello"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	}); err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}

	got := waitForSignals(t, device, 1)
	if len(got) != 1 || got[0] != SignalSuccess {
		t.Errorf("signals = %v, want one success", got)
	}
}

func TestFeedbackOnScan(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true
	tag.Data = EncodeNdefMessageWithTextRecord("scanned", "en")

	reader, device := newFeedbackTestReader(t, tag)
	reader.SetFeedback(true)

	reader.Start()
	t.Cleanup(reader.Stop)

	// The worker blocks on the data channel until the scan is taken off it.
	go func() {
		for range reader.Data() {
		}
	}()

	got := waitForSignals(t, device, 1)
	if len(got) == 0 || got[0] != SignalSuccess {
		t.Errorf("signals = %v, want a success on the first scan", got)
	}
}

func TestFeedbackOnFailedWrite(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true
	tag.WriteDataError = errors.New("tag refused the write")

	reader, device := newFeedbackTestReader(t, tag)
	reader.SetFeedback(true)

	if _, err := reader.WriteMessageWithResult(textMessage("hello"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	}); err == nil {
		t.Fatal("WriteMessageWithResult() succeeded, want the write to fail")
	}

	got := waitForSignals(t, device, 1)
	if len(got) != 1 || got[0] != SignalFailure {
		t.Errorf("signals = %v, want one failure", got)
	}
}

func TestFeedbackSurvivesAnUnsupportedReader(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	tag.IsConnected = true

	reader, device := newFeedbackTestReader(t, tag)
	device.SignalError = NewNotSupportedError("Signal")
	reader.SetFeedback(true)

	result, err := reader.WriteMessageWithResult(textMessage("hello"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}
	if result == nil || !result.Verified {
		t.Error("a reader that cannot signal should still report a verified write")
	}
}
