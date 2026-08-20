package clientserver

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// recordingOps captures the target an operation was asked for.
type recordingOps struct{ seen chan server.Target }

func newRecordingOps() *recordingOps {
	return &recordingOps{seen: make(chan server.Target, 4)}
}

func (o *recordingOps) record(target server.Target) {
	select {
	case o.seen <- target:
	default:
	}
}

func (o *recordingOps) Write(_ context.Context, req server.WriteOp) (*nfc.WriteResult, error) {
	o.record(req.Target)
	return &nfc.WriteResult{}, nil
}
func (o *recordingOps) Lock(_ context.Context, req server.LockOp) (*nfc.LockResult, error) {
	o.record(req.Target)
	return &nfc.LockResult{}, nil
}
func (o *recordingOps) Transceive(_ context.Context, req server.TransceiveOp) ([]byte, error) {
	o.record(req.Target)
	return nil, nil
}
func (o *recordingOps) Capabilities(_ context.Context, req server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	o.record(req.Target)
	return &nfc.TagCapabilities{}, nil
}

func (o *recordingOps) await(t *testing.T) server.Target {
	t.Helper()
	select {
	case target := <-o.seen:
		return target
	case <-time.After(2 * time.Second):
		t.Fatal("the request never reached the operation")
		return server.Target{}
	}
}

// The target a browser puts in its request has to survive the trip to the
// operation, or the agent resolves against nothing and refuses every write.
// These names are easy to get wrong silently: the fields are optional, so a
// typo reads as "the client named no tag".
//
// Sent over a real connection, because that is the only path that exercises the
// JSON names the client library actually writes.
func TestRequestPayloadTargetReachesTheOperation(t *testing.T) {
	const uid = "04:A1:B2:C3"
	const device = "phone-1"

	cases := []struct {
		name    string
		msgType string
		payload map[string]any
	}{
		{"write", "writeRequest", map[string]any{
			"records":         []map[string]any{{"type": "text", "content": "x"}},
			"uid":             uid,
			"deviceID":        device,
			"allowUntargeted": true,
		}},
		{"lock", "lockRequest", map[string]any{
			"uid": uid, "deviceID": device, "allowUntargeted": true,
		}},
		{"transceive", "transceiveRequest", map[string]any{
			"data": "MAA=", "uid": uid, "deviceID": device, "allowUntargeted": true,
		}},
		{"capabilities", "capabilitiesRequest", map[string]any{
			"uid": uid, "deviceID": device, "allowUntargeted": true,
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ops := newRecordingOps()
			s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
			conn := dial(t, s, "https://app.example.com")

			_ = conn.WriteJSON(map[string]any{"type": c.msgType, "payload": c.payload})

			target := ops.await(t)
			if target.TagUID != uid {
				t.Errorf("TagUID = %q, want %q", target.TagUID, uid)
			}
			if target.DeviceID != device {
				t.Errorf("DeviceID = %q, want %q", target.DeviceID, device)
			}
			if !target.AllowUntargeted {
				t.Error("AllowUntargeted did not survive the trip")
			}
		})
	}
}

// A client asking for an operation before the agent serves them gets a refusal
// rather than a panic.
func TestOperationsRefusedWithoutOps(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}})

	if _, err := s.ops().Write(context.Background(), server.WriteOp{}); err == nil {
		t.Error("a write succeeded with nothing to perform it")
	}
}
