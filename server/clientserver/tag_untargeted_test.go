package clientserver

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// allowUntargeted is the per-request opt-out, for a client that cannot name its
// tag. Asked for on the request rather than enabled by the operator, so one
// such client does not lower the guarantee for every other client on the agent.
func TestUntargetedWriteIsServedWhenAllowed(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWriteFor("legacy-1", "")
	msg.AllowUntargeted = true
	_ = goWrite(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)
	if got, _ := req.Payload["tagUID"].(string); got != scannedUID {
		t.Errorf("tagUID = %q, want the most recent scan", got)
	}
}

// Even with allowUntargeted set, a request that does name a tag is still held
// to it: the opt-in widens what an unnamed request may do, not a named one.
func TestNamedWriteStillCheckedWhenUntargetedAllowed(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWriteFor("legacy-2", "04:DE:AD:00")
	msg.AllowUntargeted = true
	done := goWrite(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Fatal("write succeeded against a tag nobody is holding")
		}
		if resp.ErrorCode != protocol.ErrCodeNoCard {
			t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeNoCard)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no response")
	}

	assertNoWriteRequest(t, conn)
}
