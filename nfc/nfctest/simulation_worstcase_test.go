package nfctest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// These simulations model the ways a real tap goes wrong: a card lifted before a
// write lands, a noisy field that NAKs the first attempts, silicon that
// acknowledges a write it never persisted, a tag swapped for another between the
// moment a client reads it and the moment it writes, and a locked or overfull
// tag. The contract under test is the same throughout: the agent must never
// report a write that did not land as a success, and must fail with a typed
// error the caller can act on rather than a silent or misclassified one.

var overwrite = nfc.WriteOptions{Overwrite: true, Index: -1}

// TestWorstCase_CardLiftedBeforeWrite: a client sees the tap, decides to write,
// but the card is gone by the time the write runs. The write must fail rather
// than block or claim a phantom success against no card.
func TestWorstCase_CardLiftedBeforeWrite(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("seed")
	reader := NewEmulatedReader(t, card)

	reader.Remove(card.UID())

	res, err := reader.WriteMessage(textMessage("late"), overwrite)
	if err == nil {
		t.Fatalf("expected an error writing to a lifted card, got result %+v", res)
	}
	if res != nil {
		t.Errorf("expected no result on a failed write, got %+v", res)
	}
}

// TestWorstCase_FlakyFieldRecovers: the field NAKs the first two write attempts,
// then settles. Within its three-attempt budget the write must still land and
// verify — a transient NAK is not a reason to fail the tap.
func TestWorstCase_FlakyFieldRecovers(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("seed").FailingWrites(2)
	reader := NewEmulatedReader(t, card)

	res, err := reader.WriteMessage(textMessage("through the noise"), overwrite)
	if err != nil {
		t.Fatalf("write should recover from 2 transient NAKs within %d attempts: %v", nfc.DefaultMaxWriteAttempts, err)
	}
	if !res.Verified {
		t.Errorf("recovered write should be verified, got %+v", res)
	}
	if res.Attempts != 3 {
		t.Errorf("expected the write to take 3 attempts (2 NAK + 1 success), got %d", res.Attempts)
	}

	if !cardHolds(t, card, "through the noise") {
		t.Error("card should hold the recovered payload after retries")
	}
}

// TestWorstCase_FieldNeverSettles: every attempt NAKs. The write must exhaust
// its budget and return an error, never a partial or unverified success.
func TestWorstCase_FieldNeverSettles(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("seed").FailingWrites(99)
	reader := NewEmulatedReader(t, card)

	res, err := reader.WriteMessage(textMessage("doomed"), overwrite)
	if err == nil {
		t.Fatalf("expected an error when every write attempt NAKs, got %+v", res)
	}
	if res != nil {
		t.Errorf("a failed write must not return a result, got %+v", res)
	}

	// The original content must survive a write that never landed.
	if !cardHolds(t, card, "seed") {
		t.Error("the seed must survive a write that never landed")
	}
}

// TestWorstCase_TagAcknowledgesButDoesNotPersist: the tag corrupts every write,
// so the read-back never matches. Verification must catch this and report a
// failure rather than a false success — this is the whole reason writes are
// verified.
func TestWorstCase_TagAcknowledgesButDoesNotPersist(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("seed").Corrupting()
	reader := NewEmulatedReader(t, card)

	res, err := reader.WriteMessage(textMessage("wishful"), overwrite)
	if err == nil {
		t.Fatalf("expected verification to fail on a tag that does not persist, got %+v", res)
	}
	if res != nil {
		t.Errorf("an unverified write must not be reported as a result, got %+v", res)
	}
}

// TestWorstCase_TagSwappedUnderClient: a client reads tag A and asks to write to
// A by UID, but B is now on the reader. The write must refuse rather than encode
// one card's data onto another — the wrong-card write is the costly mistake this
// guard exists to prevent.
func TestWorstCase_TagSwappedUnderClient(t *testing.T) {
	a := NTAG215("04AAAAAAAAAAAA").WithText("card-A")
	b := NTAG215("04BBBBBBBBBBBB").WithText("card-B")

	reader := NewEmulatedReader(t, a)
	reader.Remove(a.UID())
	reader.Present(b)

	// Client still believes A is present and writes expecting A's UID.
	optsA := overwrite
	optsA.ExpectUID = a.UID()
	res, err := reader.Supervisor.WriteMessage(t.Context(), "", textMessage("meant for A"), optsA)
	if err == nil {
		t.Fatalf("expected refusal writing to A while B is present, got %+v", res)
	}

	// B must be untouched by a write that named A.
	if !cardHolds(t, b, "card-B") {
		t.Error("B must be untouched by a write meant for A")
	}
}

// TestWorstCase_TwoTagsOnOneReader: two cards land in the field at once. An
// unqualified write is ambiguous and must be refused rather than picking one
// arbitrarily.
func TestWorstCase_TwoTagsOnOneReader(t *testing.T) {
	a := NTAG215("04AAAAAAAAAAAA").WithText("A")
	b := NTAG215("04BBBBBBBBBBBB").WithText("B")
	reader := NewEmulatedReader(t, a, b)

	res, err := reader.WriteMessage(textMessage("which one?"), overwrite)
	if err == nil {
		t.Fatalf("expected refusal with two tags present, got %+v", res)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "multiple") {
		t.Errorf("expected a multiple-tags error, got: %v", err)
	}
}

// TestWorstCase_WriteToLockedTag: a locked tag must reject a write as a permanent
// read-only error, not churn through the retry budget hoping it turns writable.
func TestWorstCase_WriteToLockedTag(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("frozen").Locked()
	reader := NewEmulatedReader(t, card)

	res, err := reader.WriteMessage(textMessage("no entry"), overwrite)
	if err == nil {
		t.Fatalf("expected a read-only error writing to a locked tag, got %+v", res)
	}
	if !nfc.IsReadOnlyError(err) {
		t.Errorf("expected a typed read-only error, got: %v", err)
	}
}

// TestWorstCase_MessageTooBigForTag: a payload past the tag's NDEF capacity must
// be refused up front by the capacity check, not written partially.
func TestWorstCase_MessageTooBigForTag(t *testing.T) {
	// NTAG213 has the smallest user memory of the family.
	card := NTAG213("04A1B2C3D4E5F6")
	reader := NewEmulatedReader(t, card)

	res, err := reader.WriteMessage(textMessage(strings.Repeat("x", 4096)), overwrite)
	if err == nil {
		t.Fatalf("expected a capacity error for an oversized payload, got %+v", res)
	}
	if !nfc.IsCapacityExceededError(err) {
		t.Errorf("expected a typed capacity-exceeded error, got: %v", err)
	}
}

// cardHolds reports whether the card, read directly through its driver, holds
// exactly the single text record want. Comparing the encoded NDEF bytes avoids a
// decode step and is exact.
func cardHolds(t *testing.T, card *EmulatedCard, want string) bool {
	t.Helper()
	got, err := card.Tag().ReadData()
	if err != nil {
		t.Fatalf("reading %s back: %v", card.UID(), err)
	}
	expected, err := textMessage(want).Encode()
	if err != nil {
		t.Fatalf("encoding expected message: %v", err)
	}
	return bytes.Equal(got, expected)
}
