package nfc

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestTransceiveRaw_ReachesTheTagAsAFrame(t *testing.T) {
	rig := newLeaseRig(t)
	var got []byte
	rig.tag.TransceiveRawFunc = func(frame []byte) ([]byte, error) {
		got = append([]byte(nil), frame...)
		return []byte{0xDE, 0xAD}, nil
	}

	reply, err := rig.reader.TransceiveRawExpecting(t.Context(), []byte{0x3C, 0x00}, "04A1B2C3")
	if err != nil {
		t.Fatalf("TransceiveRawExpecting: %v", err)
	}
	if !bytes.Equal(reply, []byte{0xDE, 0xAD}) {
		t.Errorf("reply = % X, want DE AD", reply)
	}
	if !bytes.Equal(got, []byte{0x3C, 0x00}) {
		t.Errorf("tag saw frame % X, want 3C 00", got)
	}
	if rig.sent.Load() != 0 {
		t.Error("the frame also went out as an APDU exchange")
	}
}

func TestTransceiveRaw_RefusesAsNotSupportedNeverAnAPDU(t *testing.T) {
	rig := newLeaseRig(t)

	_, err := rig.reader.TransceiveRawExpecting(t.Context(), []byte{0x3C, 0x00}, "")
	if !IsNotSupportedError(err) {
		t.Fatalf("err = %v, want a not-supported error", err)
	}
	if n := rig.sent.Load(); n != 0 {
		t.Errorf("%d APDU exchanges happened, want none", n)
	}
}

func TestTransceiveRaw_RefusesAnotherTag(t *testing.T) {
	rig := newLeaseRig(t)
	rig.tag.TransceiveRawFunc = func([]byte) ([]byte, error) {
		t.Error("a frame reached a tag the caller did not name")
		return nil, nil
	}

	_, err := rig.reader.TransceiveRawExpecting(t.Context(), []byte{0x30, 0x00}, "04FFFFFF")
	if !errors.Is(err, ErrTagUIDMismatch) {
		t.Errorf("err = %v, want ErrTagUIDMismatch", err)
	}
}

func TestTransceiveRaw_RefusesAnEmptyFrame(t *testing.T) {
	rig := newLeaseRig(t)

	if _, err := rig.reader.TransceiveRawExpecting(t.Context(), nil, ""); err == nil {
		t.Error("an empty frame was accepted")
	}
}

// A raw exchange is tag I/O like any other: it waits for a lease to end rather
// than reaching the card inside it.
func TestTransceiveRaw_WaitsForTheOperationSlot(t *testing.T) {
	rig := newLeaseRig(t)
	rig.tag.TransceiveRawFunc = func([]byte) ([]byte, error) { return []byte{0x01}, nil }
	ctx := t.Context()

	id, err := rig.reader.BeginRawSession(ctx, "", MaxRawSessionTTL)
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := rig.reader.TransceiveRawExpecting(ctx, []byte{0x30, 0x00}, "")
		result <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("a raw exchange ran inside the lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := rig.reader.EndRawSession(id); err != nil {
		t.Fatalf("EndRawSession: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("raw exchange after the lease: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the raw exchange never ran after the lease ended")
	}
}

func TestSupervisorTransceiveTag_RawSelectsTheFramingExchange(t *testing.T) {
	m := NewMockManager()
	m.DevicesList = []string{"mock:usb:001"}
	m.MockDevice = NewMockDevice()
	tag := NewMockTag("04A1B2C3")
	tag.IsConnected = true
	tag.TransceiveRawFunc = func([]byte) ([]byte, error) { return []byte{0x0A}, nil }
	m.MockDevice.SetTags([]Tag{tag})
	s := startedSupervisor(t, m)

	frame, err := s.TransceiveTag(context.Background(), "mock:usb:001", "04A1B2C3", []byte{0x30, 0x00}, true)
	if err != nil || !bytes.Equal(frame, []byte{0x0A}) {
		t.Fatalf("framing exchange = % X, %v", frame, err)
	}
}
