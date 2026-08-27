package nfc

import (
	"context"
	"errors"
	"testing"
)

// A lock cannot be undone, so it must reach the tag the caller named and no
// other. The check runs inside the tag operation, against the tag actually
// present, rather than against a status polled earlier.
func TestLockCardExpectingRefusesAnotherTag(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.LockCardExpecting(context.Background(), "04FFFFFF")
	if err == nil {
		t.Fatal("locked a tag the caller did not name")
	}
	if !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("err = %v, want ErrTagUIDMismatch", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
	if mockTag.IsReadOnly {
		t.Error("the tag was locked anyway, which cannot be undone")
	}
}

func TestLockCardExpectingAcceptsTheNamedTag(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	if _, err := reader.LockCardExpecting(context.Background(), "04A1B2C3"); err != nil {
		t.Fatalf("LockCardExpecting: %v", err)
	}
	if !mockTag.IsReadOnly {
		t.Error("the named tag was not locked")
	}
}

// The same guard on the write path: a payload encoded for one tag must not be
// written onto another.
func TestWriteExpectUIDRefusesAnotherTag(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	_, err := reader.WriteMessageWithResult(context.Background(), textMessage("payload for a different tag"),
		WriteOptions{Overwrite: true, Index: -1, ExpectUID: "04FFFFFF"})
	if err == nil {
		t.Fatal("wrote a payload to a tag the caller did not name")
	}
	if !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("err = %v, want ErrTagUIDMismatch", err)
	}
}

func TestWriteExpectUIDAcceptsTheNamedTag(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	if _, err := reader.WriteMessageWithResult(context.Background(), textMessage("hello"),
		WriteOptions{Overwrite: true, Index: -1, ExpectUID: "04A1B2C3"}); err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
}

// An empty ExpectUID keeps the old behaviour, which is what the reader's own
// callers (the tray, a program driving it directly) still rely on.
func TestEmptyExpectUIDActsOnWhateverIsPresent(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	if _, err := reader.WriteMessageWithResult(context.Background(), textMessage("hi"),
		WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
}
