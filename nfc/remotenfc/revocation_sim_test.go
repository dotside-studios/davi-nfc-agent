package remotenfc

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestPhone_RevocationFailsInFlightWrite covers credential revocation while a tag
// operation is in flight: an operator revokes a device (DisconnectDevice, which
// is what carries a credential change to an already-connected device) just as a
// write to it is waiting for the phone's answer. The waiter must be released
// promptly with a typed disconnect error rather than left blocking for the full
// 20s device-write timeout — a revoked device must not be able to hold an
// operation open.
func TestPhone_RevocationFailsInFlightWrite(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone, deviceID := connectDevice(t, url)

	writeErr := make(chan error, 1)
	go func() {
		msg := nfc.NewNDEFMessage()
		msg.AddRecord((&nfc.NDEFText{Content: "x", Language: "en"}).ToRecord())
		_, err := m.WriteTag(context.Background(), deviceID, "04A1B2C3", msg, false, "k")
		writeErr <- err
	}()

	// The phone receives the write, so the request is genuinely in flight.
	var req protocol.WebSocketRequest
	_ = phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := phone.ReadJSON(&req); err != nil {
		t.Fatalf("phone did not receive the write request: %v", err)
	}

	// The operator revokes the device mid-write.
	if !m.DisconnectDevice(deviceID, "credential revoked") {
		t.Fatal("expected the device to be connected when revoked")
	}

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("a write in flight to a revoked device must fail, not report success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight write was not released promptly when the device was revoked (held past the fail-fast window)")
	}

	// The device is gone from the registry after revocation.
	waitFor(t, 3*time.Second, func() bool {
		_, ok := m.GetDevice(deviceID)
		return !ok
	}, "the revoked device to be unregistered")
}

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
