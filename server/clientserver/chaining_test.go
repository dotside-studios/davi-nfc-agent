package clientserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

const chainLane = "lane1"

var (
	chainGetChunked = []byte{0x00, 0xCA, nfctest.ChainP1Chunked, 0x00, 0x00}
	chainGetShortLe = []byte{0x00, 0xCA, nfctest.ChainP1ShortLe, 0x00, 0x00}
)

func chainingOps(t *testing.T) *tagOps {
	t.Helper()
	lanes := nfctest.NewEmulatedLanes(t, chainLane)
	lanes.SetMode(nfc.ModeReadWrite)
	lanes.Present(chainLane, nfctest.NewChainingApplet("04A1B2C3D4E5F6").EmulatedCard)
	awaitCardOnReader(t, lanes.Supervisor, chainLane)

	return newTagOps(Config{
		Tags:                 lanes.Supervisor,
		AllowTagModification: func() bool { return true },
		AllowRawTransceive:   func() bool { return true },
	})
}

func TestTransceiveAutoGetResponseFollowsTheCard(t *testing.T) {
	s := chainingOps(t)
	target := server.Target{AllowUntargeted: true}

	got, err := s.Transceive(context.Background(), server.TransceiveOp{Target: target, Data: chainGetChunked, AutoGetResponse: true})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if want := append(nfctest.ChainChunked(), 0x90, 0x00); !bytes.Equal(got, want) {
		t.Errorf("chained reply = % X, want % X", got, want)
	}

	got, err = s.Transceive(context.Background(), server.TransceiveOp{Target: target, Data: chainGetShortLe, AutoGetResponse: true})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if want := append(nfctest.ChainShortLe(), 0x90, 0x00); !bytes.Equal(got, want) {
		t.Errorf("corrected reply = % X, want % X", got, want)
	}
}

func TestTransceiveWithoutAutoGetResponseIsUnchanged(t *testing.T) {
	s := chainingOps(t)
	target := server.Target{AllowUntargeted: true}

	got, err := s.Transceive(context.Background(), server.TransceiveOp{Target: target, Data: chainGetChunked})
	if err != nil || !bytes.Equal(got, []byte{0x61, 0x10}) {
		t.Errorf("reply = % X, %v, want 61 10", got, err)
	}
	got, err = s.Transceive(context.Background(), server.TransceiveOp{Target: target, Data: chainGetShortLe})
	if err != nil || !bytes.Equal(got, []byte{0x6C, 0x08}) {
		t.Errorf("reply = % X, %v, want 6C 08", got, err)
	}
}

func TestTransceiveAutoGetResponseInsideASession(t *testing.T) {
	s := chainingOps(t)
	ctx := context.Background()

	lease, err := s.BeginRawSession(ctx, server.RawSessionBeginOp{Target: server.Target{AllowUntargeted: true}})
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}
	defer func() { _ = s.EndRawSession(ctx, lease.SessionID) }()

	got, err := s.Transceive(ctx, server.TransceiveOp{SessionID: lease.SessionID, Data: chainGetChunked, AutoGetResponse: true})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if want := append(nfctest.ChainChunked(), 0x90, 0x00); !bytes.Equal(got, want) {
		t.Errorf("chained reply = % X, want % X", got, want)
	}
}

func TestTransceiveSequenceAutoGetResponsePerStep(t *testing.T) {
	s := chainingOps(t)

	res, err := s.TransceiveSequence(context.Background(), server.SequenceOp{
		Target: server.Target{AllowUntargeted: true},
		Steps: []nfc.SequenceStep{
			{Data: chainGetChunked, AutoGetResponse: true},
			{Data: chainGetShortLe},
		},
	})
	if err != nil {
		t.Fatalf("TransceiveSequence: %v", err)
	}
	if !bytes.Equal(res.Replies[0], append(nfctest.ChainChunked(), 0x90, 0x00)) {
		t.Errorf("chained step = % X", res.Replies[0])
	}
	if !bytes.Equal(res.Replies[1], []byte{0x6C, 0x08}) {
		t.Errorf("plain step = % X, want 6C 08", res.Replies[1])
	}
}

// Each command sent after the client's own is logged, as part of the exchange
// it followed, and never with its bytes.
func TestAutoGetResponseFollowUpsAreAudited(t *testing.T) {
	var logged bytes.Buffer
	restore := captureClientLogs(&logged)
	defer restore()

	s := chainingOps(t)
	if _, err := s.Transceive(context.Background(), server.TransceiveOp{
		Target: server.Target{AllowUntargeted: true}, Data: chainGetChunked, AutoGetResponse: true,
	}); err != nil {
		t.Fatal(err)
	}

	text := logged.String()
	if n := strings.Count(text, "Raw exchange follow-up (autoGetResponse)"); n != 2 {
		t.Errorf("%d follow-up lines logged, want 2 for the two GET RESPONSE commands:\n%s", n, text)
	}
	if !strings.Contains(text, "GET RESPONSE") {
		t.Errorf("follow-ups are not named as GET RESPONSE:\n%s", text)
	}
}

func TestAutoGetResponseOverTheWire(t *testing.T) {
	ops := &echoOps{reply: []byte{0x01, 0x90, 0x00}, seen: make(chan server.TransceiveOp, 2)}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")
	data := base64.StdEncoding.EncodeToString(chainGetChunked)

	for _, tc := range []struct {
		payload map[string]any
		want    bool
	}{
		{map[string]any{"data": data}, false},
		{map[string]any{"data": data, "autoGetResponse": true}, true},
	} {
		if err := conn.WriteJSON(map[string]any{"id": "r", "type": "transceiveRequest", "payload": tc.payload}); err != nil {
			t.Fatal(err)
		}
		if msg := readResponse(t, conn); msg["success"] != true {
			t.Fatalf("response = %v", msg)
		}
		select {
		case op := <-ops.seen:
			if op.AutoGetResponse != tc.want {
				t.Errorf("AutoGetResponse = %v, want %v", op.AutoGetResponse, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the operation never saw the request")
		}
	}

	if err := conn.WriteJSON(map[string]any{"id": "r", "type": "transceiveRequest",
		"payload": map[string]any{"data": data, "raw": true, "autoGetResponse": true}}); err != nil {
		t.Fatal(err)
	}
	if msg := readResponse(t, conn); msg["type"] != "error" {
		t.Errorf("raw with autoGetResponse: response = %v, want an error", msg)
	}
}

func TestAutoGetResponseOnSequenceStepsOverTheWire(t *testing.T) {
	ops := &sequenceOps{res: &nfc.SequenceResult{Replies: [][]byte{{0x90, 0x00}, {0x90, 0x00}}, StoppedAt: -1}}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")
	data := base64.StdEncoding.EncodeToString(chainGetChunked)

	if err := conn.WriteJSON(map[string]any{"id": "r", "type": "transceiveSequenceRequest", "payload": map[string]any{
		"steps": []map[string]any{{"data": data, "autoGetResponse": true}, {"data": data}},
	}}); err != nil {
		t.Fatal(err)
	}
	if msg := readResponse(t, conn); msg["success"] != true {
		t.Fatalf("response = %v", msg)
	}

	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.seen) != 1 || !ops.seen[0].Steps[0].AutoGetResponse || ops.seen[0].Steps[1].AutoGetResponse {
		t.Errorf("steps = %+v, want autoGetResponse on the first only", ops.seen)
	}
}

// phoneHolder holds a tag the agent can only reach by exchanging bytes with a
// phone. It is a TagHolder and nothing more: no lease, no sequence.
type phoneHolder struct {
	mu   sync.Mutex
	sent [][]byte
	next [][]byte
}

func (p *phoneHolder) TagOn(string) (string, string, bool) { return "phone:1", "04A1B2C3", true }
func (p *phoneHolder) DevicesHoldingTags() []string        { return []string{"phone:1"} }
func (p *phoneHolder) WriteTag(context.Context, string, string, *nfc.NDEFMessage, bool, string) (*nfc.WriteResult, error) {
	return nil, nfc.NewNotSupportedError("WriteTag")
}
func (p *phoneHolder) LockTag(context.Context, string, string, string) (*nfc.LockResult, error) {
	return nil, nfc.NewNotSupportedError("LockTag")
}
func (p *phoneHolder) TagCapabilities(context.Context, string, string) (*nfc.TagCapabilities, error) {
	return &nfc.TagCapabilities{CanTransceive: true}, nil
}
func (p *phoneHolder) TransceiveTag(_ context.Context, _, _ string, data []byte, _ bool) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, append([]byte(nil), data...))
	reply := p.next[0]
	p.next = p.next[1:]
	return reply, nil
}

// A tag held through a phone is chained over repeated device exchanges, one
// round trip to the phone each.
func TestAutoGetResponseOverAPhoneRepeatsDeviceExchanges(t *testing.T) {
	phone := &phoneHolder{next: [][]byte{{0x61, 0x02}, {0x01, 0x02, 0x90, 0x00}}}
	s := newTagOps(Config{
		Tags:                 phone,
		AllowTagModification: func() bool { return true },
		AllowRawTransceive:   func() bool { return true },
	})

	got, err := s.Transceive(context.Background(), server.TransceiveOp{
		Target: server.Target{DeviceID: "phone:1"}, Data: chainGetChunked, AutoGetResponse: true,
	})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if !bytes.Equal(got, []byte{0x01, 0x02, 0x90, 0x00}) {
		t.Errorf("reply = % X", got)
	}
	if len(phone.sent) != 2 || !bytes.Equal(phone.sent[1], []byte{0x00, 0xC0, 0x00, 0x00, 0x02}) {
		t.Errorf("phone was sent % X, want the command, then GET RESPONSE for 2", phone.sent)
	}
}
