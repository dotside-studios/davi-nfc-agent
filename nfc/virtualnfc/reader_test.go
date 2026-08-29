package virtualnfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// waitForTag blocks until the reader's Supervisor is holding a tag on the device,
// so a test does not race the background poll that discovers a presented tag.
func waitForTag(t *testing.T, r *Reader, device string) *nfc.TagCapabilities {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if caps, err := r.Capabilities(device); err == nil {
			return caps
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("virtualnfc: no tag seen on %q within 3s", device)
	return nil
}

func newReader(t *testing.T, names ...string) *Reader {
	t.Helper()
	r, err := NewReader(names...)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	t.Cleanup(r.Close)
	return r
}

func TestReaderWritableTagCapabilities(t *testing.T) {
	r := newReader(t)
	if err := r.Present("", TagSpec{UID: "04:A1:B2:C3", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	caps := waitForTag(t, r, "")
	if !caps.CanRead || !caps.CanWrite || !caps.CanLock {
		t.Errorf("caps = %+v, want CanRead, CanWrite, CanLock", caps)
	}
	if !caps.ReadsAreSnapshot {
		t.Error("routed tag should report ReadsAreSnapshot")
	}
}

func TestReaderWriteRoutesToStore(t *testing.T) {
	r := newReader(t)
	if err := r.Present("", TagSpec{UID: "04:11:22:33"}); err != nil { // blank, writable
		t.Fatal(err)
	}
	waitForTag(t, r, "")

	msg, err := TextMessage("written", "")
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	if _, err := r.Write("", msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want, _ := msg.Encode()
	if got := r.route.Content("04:11:22:33"); string(got) != string(want) {
		t.Errorf("stored content = %x, want %x", got, want)
	}
}

func TestReaderReadOnlyTagRefusesWrite(t *testing.T) {
	r := newReader(t)
	if err := r.Present("", TagSpec{UID: "04:DE:AD", Text: "sealed", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}

	caps := waitForTag(t, r, "")
	if caps.CanWrite || caps.CanLock {
		t.Errorf("read-only caps = %+v, want no write/lock", caps)
	}
	if !caps.IsReadOnly {
		t.Error("caps.IsReadOnly = false, want true")
	}

	msg, _ := TextMessage("nope", "")
	if _, err := r.Write("", msg); err == nil {
		t.Error("Write to read-only tag succeeded, want error")
	}
}

func TestReaderScanEmitsNFCData(t *testing.T) {
	r := newReader(t)

	scanned := make(chan *nfc.Card, 4)
	conn := r.Supervisor.Scans().Connect(func(d nfc.NFCData) {
		if d.Card != nil {
			scanned <- d.Card
		}
	})
	defer conn.Disconnect()

	if err := r.Present("", TagSpec{UID: "04:CA:FE", URI: "https://example.com"}); err != nil {
		t.Fatal(err)
	}

	select {
	case card := <-scanned:
		if card.UID != "04:CA:FE" {
			t.Errorf("scanned UID = %q, want 04:CA:FE", card.UID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no scan within 3s")
	}
}

func TestReaderRemoveForgetsTag(t *testing.T) {
	r := newReader(t)
	if err := r.Present("", TagSpec{UID: "04:99", Text: "bye"}); err != nil {
		t.Fatal(err)
	}
	waitForTag(t, r, "")

	if err := r.Remove("", "04:99"); err != nil {
		t.Fatal(err)
	}
	if got := r.route.Content("04:99"); got != nil {
		t.Errorf("content after remove = %x, want nil", got)
	}
}

func TestReaderMultipleDevices(t *testing.T) {
	r := newReader(t, "entry", "exit")
	if got := len(r.Devices()); got != 2 {
		t.Fatalf("Devices() = %d, want 2", got)
	}
	if err := r.Present("entry", TagSpec{UID: "04:EN", Text: "member"}); err != nil {
		t.Fatal(err)
	}
	waitForTag(t, r, "entry")

	// The exit lane holds nothing.
	if _, err := r.Capabilities("exit"); err == nil {
		t.Error("exit lane reported a tag, want none")
	}
}

func TestReaderUnknownDeviceAndMissingUID(t *testing.T) {
	r := newReader(t)
	if err := r.Present("nope", TagSpec{UID: "04:01"}); err == nil {
		t.Error("present to unknown device succeeded, want error")
	}
	if err := r.Present("", TagSpec{}); err == nil {
		t.Error("present with no UID succeeded, want error")
	}
}
