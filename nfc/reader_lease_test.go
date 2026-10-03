package nfc

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type leaseRig struct {
	reader *deviceReader
	device *MockDevice
	tag    *MockTag
	clock  *FakeClock
	sent   atomic.Int32
}

func newLeaseRig(t *testing.T) *leaseRig {
	t.Helper()

	rig := &leaseRig{}
	rig.tag = NewMockTag("04A1B2C3")
	rig.tag.TagType = "Type4A"
	rig.tag.IsConnected = true
	rig.tag.Data = EncodeNdefMessageWithTextRecord("hello", "en")
	rig.tag.TransceiveFunc = func(data []byte) ([]byte, error) {
		rig.sent.Add(1)
		return []byte{0x01, 0x91, 0xAF}, nil
	}

	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	rig.device = NewMockDevice()
	rig.device.SetTags([]Tag{rig.tag})
	manager.MockDevice = rig.device

	rig.clock = NewFakeClock(time.Now())
	reader, err := newDeviceReaderWithClock("mock:usb:001", manager, 5*time.Second, rig.clock)
	if err != nil {
		t.Fatalf("newDeviceReaderWithClock: %v", err)
	}
	t.Cleanup(reader.Close)
	rig.reader = reader
	return rig
}

func (r *leaseRig) cardCalls() int {
	r.tag.mu.Lock()
	defer r.tag.mu.Unlock()
	n := 0
	for _, call := range r.tag.CallLog {
		if call != "Capabilities" {
			n++
		}
	}
	return n
}

func (r *leaseRig) getTagsCalls() int {
	r.device.mu.Lock()
	defer r.device.mu.Unlock()
	n := 0
	for _, call := range r.device.CallLog {
		if call == "GetTags" {
			n++
		}
	}
	return n
}

func (r *leaseRig) readCalls() int {
	r.tag.mu.Lock()
	defer r.tag.mu.Unlock()
	n := 0
	for _, call := range r.tag.CallLog {
		if call == "ReadData" {
			n++
		}
	}
	return n
}

func isRawSessionExpired(err error) bool {
	var nfcErr *NFCError
	return errors.As(err, &nfcErr) && nfcErr.Code == ErrCodeRawSessionExpired
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPoll_PublishedCardIsNotReadAgain(t *testing.T) {
	rig := newLeaseRig(t)

	got := pollAndCollect(rig.reader, 6)

	if len(got) != 1 {
		t.Fatalf("got %d scans across 6 polls, want 1", len(got))
	}
	if n := rig.readCalls(); n != 1 {
		t.Errorf("tag was read %d times across 6 polls, want 1", n)
	}
}

func TestRawSession_PollingSendsNothingWhileHeld(t *testing.T) {
	rig := newLeaseRig(t)
	pollAndCollect(rig.reader, 1)

	ctx := t.Context()
	id, err := rig.reader.BeginRawSession(ctx, "04A1B2C3", time.Second)
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}

	before, getTagsBefore := rig.cardCalls(), rig.getTagsCalls()
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x90, 0x71, 0x00, 0x00, 0x00}); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	pollAndCollect(rig.reader, 5)
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x90, 0xAF, 0x00, 0x00, 0x00}); err != nil {
		t.Fatalf("second exchange: %v", err)
	}

	if got := rig.cardCalls() - before; got != 2 {
		t.Errorf("card saw %d calls between the exchanges, want only the client's 2", got)
	}
	if got := rig.getTagsCalls() - getTagsBefore; got != 0 {
		t.Errorf("device was asked for tags %d times while the lease was held, want 0", got)
	}

	if err := rig.reader.EndRawSession(id); err != nil {
		t.Fatalf("EndRawSession: %v", err)
	}
	if err := rig.reader.EndRawSession(id); !isRawSessionExpired(err) {
		t.Errorf("ending twice: err = %v, want a raw-session-expired error", err)
	}
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x00}); !isRawSessionExpired(err) {
		t.Errorf("exchange after end: err = %v, want a raw-session-expired error", err)
	}
}

func TestRawSession_ExpiresAfterTTLWithoutExchange(t *testing.T) {
	rig := newLeaseRig(t)
	ctx := t.Context()

	id, err := rig.reader.BeginRawSession(ctx, "", time.Second)
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}

	rig.clock.Advance(600 * time.Millisecond)
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x30}); err != nil {
		t.Fatalf("exchange inside the ttl: %v", err)
	}

	rig.clock.Advance(600 * time.Millisecond)
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x30}); err != nil {
		t.Fatalf("an exchange renews the lease: %v", err)
	}

	rig.clock.Advance(1100 * time.Millisecond)
	eventually(t, "the lease to expire", func() bool {
		_, held := rig.reader.leaseUID()
		return !held
	})
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x30}); !isRawSessionExpired(err) {
		t.Errorf("exchange after expiry: err = %v, want a raw-session-expired error", err)
	}

	if _, err := rig.reader.TransceiveExpecting(ctx, []byte{0x30}, ""); err != nil {
		t.Errorf("an ordinary exchange after expiry: %v", err)
	}
}

func TestRawSession_CardRemovalEndsIt(t *testing.T) {
	rig := newLeaseRig(t)
	ctx := t.Context()

	id, err := rig.reader.BeginRawSession(ctx, "", 0)
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}

	rig.tag.TransceiveFunc = func([]byte) ([]byte, error) {
		return nil, NewCardRemovedError(errors.New("gone"))
	}
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x30}); !IsCardRemovedError(err) {
		t.Fatalf("exchange err = %v, want card removed", err)
	}
	if _, err := rig.reader.TransceiveInSession(ctx, id, []byte{0x30}); !isRawSessionExpired(err) {
		t.Errorf("after removal: err = %v, want a raw-session-expired error", err)
	}

	id2, err := rig.reader.BeginRawSession(ctx, "", 0)
	if err != nil {
		t.Fatalf("the reader was not released: %v", err)
	}
	_ = rig.reader.EndRawSession(id2)
}

func TestRawSession_SecondLeaseWaitsThenIsBusy(t *testing.T) {
	rig := newLeaseRig(t)
	ctx := t.Context()

	first, err := rig.reader.BeginRawSession(ctx, "", MaxRawSessionTTL)
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := rig.reader.BeginRawSession(ctx, "", 0)
		result <- err
	}()
	opResult := make(chan error, 1)
	go func() {
		_, err := rig.reader.TransceiveExpecting(ctx, []byte{0x30}, "")
		opResult <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("second lease did not wait: %v", err)
	case err := <-opResult:
		t.Fatalf("an ordinary exchange ran inside the lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	for range 2 {
		var err error
		eventually(t, "the waiter to give up", func() bool {
			rig.clock.Advance(rig.reader.operationTimeout)
			select {
			case err = <-result:
				return true
			case err = <-opResult:
				return true
			default:
				return false
			}
		})
		if !errors.Is(err, ErrReaderBusy) {
			t.Errorf("waiter err = %v, want ErrReaderBusy", err)
		}
	}

	if err := rig.reader.EndRawSession(first); err != nil {
		t.Fatalf("EndRawSession: %v", err)
	}
	id, err := rig.reader.BeginRawSession(ctx, "", 0)
	if err != nil {
		t.Fatalf("lease after the first ended: %v", err)
	}
	_ = rig.reader.EndRawSession(id)
}

func TestRawSession_RefusesAnotherTag(t *testing.T) {
	rig := newLeaseRig(t)

	_, err := rig.reader.BeginRawSession(t.Context(), "04FFFFFF", 0)
	if !errors.Is(err, ErrTagUIDMismatch) {
		t.Fatalf("err = %v, want ErrTagUIDMismatch", err)
	}
	if id, err := rig.reader.BeginRawSession(t.Context(), "04a1b2c3", 0); err != nil {
		t.Errorf("the reader was not released after a refusal: %v", err)
	} else {
		_ = rig.reader.EndRawSession(id)
	}
}

func TestRawSession_TTLBounds(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		0:                        DefaultRawSessionTTL,
		-time.Second:             DefaultRawSessionTTL,
		time.Second:              time.Second,
		time.Hour:                MaxRawSessionTTL,
		MaxRawSessionTTL:         MaxRawSessionTTL,
		MaxRawSessionTTL + 1:     MaxRawSessionTTL,
		DefaultRawSessionTTL + 1: DefaultRawSessionTTL + 1,
	}
	for in, want := range cases {
		if got := clampRawSessionTTL(in); got != want {
			t.Errorf("clampRawSessionTTL(%v) = %v, want %v", in, got, want)
		}
	}
}
