package nfc

import (
	"testing"
)

// snapshotTag reads back what it was given at construction, never what was
// written to it, the way a tag reported by a remote device does.
type snapshotTag struct {
	*MockTag
	snapshot []byte
}

func (t *snapshotTag) ReadData() ([]byte, error) { return t.snapshot, nil }

func (t *snapshotTag) Capabilities() TagCapabilities {
	caps := t.MockTag.Capabilities()
	caps.ReadsAreSnapshot = true
	return caps
}

// A write to a tag whose reads are a snapshot is not reported verified. Reading
// it back would compare against data the write could not have changed, so a
// verified result would be a claim with nothing behind it.
func TestSnapshotReadsAreNotVerified(t *testing.T) {
	mock := NewMockTag("04A1B2C3")
	mock.IsConnected = true
	tag := &snapshotTag{MockTag: mock, snapshot: []byte("stale")}

	result, err := WriteMessage(NewCard(tag), textMessage("fresh"), WriteOptions{Overwrite: true, Index: -1}, nil)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if result.Verified {
		t.Error("a write was reported verified against a snapshot read")
	}
	if result.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: an unverifiable write must not be retried for a mismatch", result.Attempts)
	}
}

// The ordinary case is unchanged: a tag read live confirms its own write.
func TestLiveReadsAreVerified(t *testing.T) {
	mock := NewMockTag("04A1B2C3")
	mock.IsConnected = true

	result, err := WriteMessage(NewCard(mock), textMessage("fresh"), WriteOptions{Overwrite: true, Index: -1}, nil)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if !result.Verified {
		t.Error("a write to a live tag was not verified")
	}
}
