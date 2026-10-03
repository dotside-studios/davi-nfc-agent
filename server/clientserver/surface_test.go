package clientserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

const surfaceLane = "lane1"

var (
	surfaceKey0 = bytes.Repeat([]byte{0x10}, 16)
	surfaceKey1 = bytes.Repeat([]byte{0x11}, 16)
)

func surfaceKeys() nfc.NTAG424Keys {
	return nfc.NTAG424Keys{Slots: map[byte][]byte{0: surfaceKey0, 1: surfaceKey1}}
}

// surfaceOps is the router over an emulated reader holding an NTAG 424 whose
// keys the agent holds.
func surfaceOps(t *testing.T, mode nfc.ReaderMode, raw bool, opts ...nfctest.NTAG424Option) (*tagOps, *nfctest.EmulatedCard) {
	t.Helper()
	opts = append([]nfctest.NTAG424Option{
		nfctest.NTAG424WithKeys(map[byte][]byte{0: surfaceKey0, 1: surfaceKey1}),
	}, opts...)
	card := nfctest.NTAG424("04A1B2C3D4E5F6", opts...)

	lanes := nfctest.NewEmulatedLanes(t, surfaceLane)
	lanes.SetNTAG424Keys(surfaceKeys())
	lanes.SetMode(mode)
	lanes.Present(surfaceLane, card)
	awaitCardOnReader(t, lanes.Supervisor, surfaceLane)

	return newTagOps(Config{
		Tags:                 lanes.Supervisor,
		AllowTagModification: func() bool { return lanes.Mode() != nfc.ModeReadOnly },
		AllowRawTransceive:   func() bool { return raw },
	}), card
}

func ntag424Req(r protocol.NTAG424RequestPayload) server.NTAG424Op {
	return server.NTAG424Op{Target: server.Target{AllowUntargeted: true}, Request: r}
}

func TestNTAG424ReadOps(t *testing.T) {
	s, _ := surfaceOps(t, nfc.ModeReadOnly, false, nfctest.NTAG424WithKeyVersion(1, 7))
	ctx := context.Background()

	res, err := s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "getFileSettings"}))
	if err != nil {
		t.Fatalf("getFileSettings: %v", err)
	}
	if res.FileSettings == nil || res.FileSettings.CommMode != "plain" {
		t.Errorf("file settings = %+v", res.FileSettings)
	}

	res, err = s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "getCardUID"}))
	if err != nil || res.UID != "04A1B2C3D4E5F6" {
		t.Errorf("getCardUID = %+v, %v", res, err)
	}

	res, err = s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "getKeyVersion", KeyNo: 1}))
	if err != nil || res.KeyVersion == nil || *res.KeyVersion != 7 {
		t.Errorf("getKeyVersion = %+v, %v", res, err)
	}

	res, err = s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "readSig"}))
	if err != nil {
		t.Fatalf("readSig: %v", err)
	}
	if sig, _ := base64.StdEncoding.DecodeString(res.Signature); len(sig) != 56 {
		t.Errorf("signature is %d bytes, want 56", len(sig))
	}
	if res.Genuine == nil || *res.Genuine {
		t.Errorf("genuine = %v, want false for the emulator's placeholder signature", res.Genuine)
	}
}

func TestNTAG424MutatingOpsRefusedInReadOnlyMode(t *testing.T) {
	s, card := surfaceOps(t, nfc.ModeReadOnly, false)
	before := card.NTAG424FileSettings(2)

	for _, r := range []protocol.NTAG424RequestPayload{
		{Op: "configureSDM", URLTemplate: "https://x.test/t?p={picc}&m={mac}"},
		{Op: "changeKey", KeyNo: 1, AuthKeyNo: 0, NewKeySource: "explicit", NewKey: strings.Repeat("22", 16), Confirm: true},
		{Op: "lock", Confirm: true},
	} {
		_, err := s.NTAG424(context.Background(), ntag424Req(r))
		if got := codeOf(err); got != protocol.ErrCodeReadOnly {
			t.Errorf("%s: code = %q, want %q", r.Op, got, protocol.ErrCodeReadOnly)
		}
	}
	if after := card.NTAG424FileSettings(2); after != before {
		t.Error("a refused operation changed the card")
	}
}

func TestNTAG424IrreversibleOpsNeedConfirm(t *testing.T) {
	s, card := surfaceOps(t, nfc.ModeReadWrite, false)
	before := card.NTAG424FileSettings(2)

	for _, r := range []protocol.NTAG424RequestPayload{
		{Op: "changeKey", KeyNo: 1, AuthKeyNo: 0, NewKeySource: "explicit", NewKey: strings.Repeat("22", 16)},
		{Op: "lock"},
	} {
		_, err := s.NTAG424(context.Background(), ntag424Req(r))
		if got := codeOf(err); got != protocol.ErrCodeInvalidRequest {
			t.Errorf("%s without confirm: code = %q, want %q", r.Op, got, protocol.ErrCodeInvalidRequest)
		}
	}
	if card.NTAG424FileSettings(2) != before {
		t.Error("an unconfirmed operation changed the card")
	}
}

func TestNTAG424ChangeKeyWithoutLeakingTheKey(t *testing.T) {
	var logged lockedBuffer
	restore := captureClientLogs(&logged)
	defer restore()

	s, card := surfaceOps(t, nfc.ModeReadWrite, false)
	newKey := bytes.Repeat([]byte{0x77}, 16)
	hexKey := hex.EncodeToString(newKey)

	res, err := s.NTAG424(context.Background(), ntag424Req(protocol.NTAG424RequestPayload{
		Op: "changeKey", KeyNo: 1, AuthKeyNo: 0, Version: 3,
		NewKeySource: "explicit", NewKey: hexKey, Confirm: true,
	}))
	if err != nil {
		t.Fatalf("changeKey: %v", err)
	}
	if !res.Changed {
		t.Error("changeKey did not report the change")
	}
	if v := card.NTAG424KeyVersion(1); v != 3 {
		t.Errorf("key version on the card = %d, want 3", v)
	}

	text := logged.String()
	if !strings.Contains(text, "NTAG 424 changeKey") {
		t.Errorf("changeKey left no audit entry: %q", text)
	}
	for _, secret := range []string{hexKey, strings.ToUpper(hexKey), base64.StdEncoding.EncodeToString(newKey)} {
		if strings.Contains(text, secret) {
			t.Errorf("the audit log carries key material: %q", text)
		}
	}
}

func TestNTAG424ChangeKeyFromConfiguredKeys(t *testing.T) {
	s, card := surfaceOps(t, nfc.ModeReadWrite, false)

	_, err := s.NTAG424(context.Background(), ntag424Req(protocol.NTAG424RequestPayload{
		Op: "changeKey", KeyNo: 0, AuthKeyNo: 0, Version: 9, NewKeySource: "configured", Confirm: true,
	}))
	if err != nil {
		t.Fatalf("changeKey: %v", err)
	}
	if v := card.NTAG424KeyVersion(0); v != 9 {
		t.Errorf("key version on the card = %d, want 9", v)
	}

	_, err = s.NTAG424(context.Background(), ntag424Req(protocol.NTAG424RequestPayload{
		Op: "changeKey", KeyNo: 0, AuthKeyNo: 0, NewKeySource: "configured", NewKey: strings.Repeat("00", 16), Confirm: true,
	}))
	if got := codeOf(err); got != protocol.ErrCodeInvalidRequest {
		t.Errorf("configured source with a key: code = %q", got)
	}
}

func TestNTAG424ConfigureSDMAndLock(t *testing.T) {
	s, card := surfaceOps(t, nfc.ModeReadWrite, false)
	ctx := context.Background()

	res, err := s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{
		Op:          "configureSDM",
		URLTemplate: "https://davi.social/t?p={picc}&m={mac}",
		SDM:         &protocol.NTAG424SDMOptions{},
	}))
	if err != nil {
		t.Fatalf("configureSDM: %v", err)
	}
	if res.SDM == nil || !res.SDM.Verified || !strings.HasPrefix(res.SDM.URL, "https://davi.social/t?p=") {
		t.Fatalf("configureSDM = %+v", res.SDM)
	}
	if !card.NTAG424FileSettings(2).SDMEnabled {
		t.Error("SDM is not enabled on the card")
	}

	fs, err := s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "getFileSettings"}))
	if err != nil || !fs.FileSettings.SDMEnabled {
		t.Errorf("getFileSettings after configureSDM = %+v, %v", fs, err)
	}

	locked, err := s.NTAG424(ctx, ntag424Req(protocol.NTAG424RequestPayload{Op: "lock", Confirm: true}))
	if err != nil || !locked.Locked {
		t.Fatalf("lock = %+v, %v", locked, err)
	}
	if got := card.NTAG424FileSettings(2); got.Write != ntag424.AccessNever {
		t.Errorf("write right after lock = %X", got.Write)
	}
}

func TestNTAG424BadRequests(t *testing.T) {
	s, _ := surfaceOps(t, nfc.ModeReadWrite, false)
	for name, r := range map[string]protocol.NTAG424RequestPayload{
		"unknown op":      {Op: "format"},
		"no template":     {Op: "configureSDM"},
		"bad template":    {Op: "configureSDM", URLTemplate: "https://x.test/{nope}"},
		"bad key number":  {Op: "getKeyVersion", KeyNo: 9},
		"short key":       {Op: "changeKey", NewKeySource: "explicit", NewKey: "00", Confirm: true},
		"no key source":   {Op: "changeKey", Confirm: true},
		"bad file number": {Op: "getFileSettings", FileNo: 7},
		"bad sdm right":   {Op: "configureSDM", URLTemplate: "https://x.test/?p={picc}&m={mac}", SDM: &protocol.NTAG424SDMOptions{Write: intp(9)}},
	} {
		_, err := s.NTAG424(context.Background(), ntag424Req(r))
		if got := codeOf(err); got != protocol.ErrCodeInvalidRequest {
			t.Errorf("%s: code = %q, want %q", name, got, protocol.ErrCodeInvalidRequest)
		}
	}
}

func intp(n int) *int { return &n }

func TestNTAG424NotSupportedOnOtherTags(t *testing.T) {
	m := nfc.NewMockManager()
	m.MockDevice.SetTags([]nfc.Tag{nfc.NewMockTag("04A1B2C3")})
	s := newTagOps(configOver(t, m, nfc.ModeReadWrite))
	awaitCardOnReader(t, s.tags, "mock:usb:001")

	_, err := s.NTAG424(context.Background(), ntag424Req(protocol.NTAG424RequestPayload{Op: "getCardUID"}))
	if got := codeOf(err); got != protocol.ErrCodeNotSupported {
		t.Errorf("code = %q, want %q", got, protocol.ErrCodeNotSupported)
	}
}

func TestCapabilitiesReportNTAG424Facts(t *testing.T) {
	s, _ := surfaceOps(t, nfc.ModeReadWrite, false)
	caps, err := s.Capabilities(context.Background(), server.CapabilitiesOp{Target: server.Target{AllowUntargeted: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.KeysHeld) != 2 || caps.KeysHeld[0] != 0 || caps.KeysHeld[1] != 1 {
		t.Errorf("keysHeld = %v, want [0 1]", caps.KeysHeld)
	}
	if caps.SDMEnabled || caps.RandomID || caps.LRP {
		t.Errorf("caps = %+v, want sdm, randomID and lrp off", caps)
	}
}

// The select by name that opens the NDEF application.
var (
	selectApp = []byte{0x00, 0xA4, 0x04, 0x00, 0x07, 0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00}
)

func TestSequenceStopRules(t *testing.T) {
	s, _ := surfaceOps(t, nfc.ModeReadWrite, true)
	target := server.Target{AllowUntargeted: true}
	run := func(steps ...nfc.SequenceStep) *nfc.SequenceResult {
		t.Helper()
		res, err := s.TransceiveSequence(context.Background(), server.SequenceOp{Target: target, Steps: steps})
		if err != nil {
			t.Fatalf("TransceiveSequence: %v", err)
		}
		return res
	}

	res := run(nfc.SequenceStep{Data: selectApp}, nfc.SequenceStep{Data: selectApp})
	if res.StoppedAt != -1 || len(res.Replies) != 2 {
		t.Fatalf("plain run = %d replies, stoppedAt %d", len(res.Replies), res.StoppedAt)
	}
	if n := len(res.Replies[0]); n < 2 || res.Replies[0][n-2] != 0x90 {
		t.Fatalf("select answered % X", res.Replies[0])
	}

	res = run(nfc.SequenceStep{Data: selectApp, StopOnSW: []uint16{0x9000}}, nfc.SequenceStep{Data: selectApp})
	if res.StoppedAt != 0 || len(res.Replies) != 1 {
		t.Errorf("stopOnSW: %d replies, stoppedAt %d, want 1 and 0", len(res.Replies), res.StoppedAt)
	}

	res = run(nfc.SequenceStep{Data: selectApp, ExpectSW: []uint16{0x9000}}, nfc.SequenceStep{Data: selectApp})
	if res.StoppedAt != -1 || len(res.Replies) != 2 {
		t.Errorf("met expectSW stopped the run: %d replies, stoppedAt %d", len(res.Replies), res.StoppedAt)
	}

	res = run(nfc.SequenceStep{Data: selectApp, ExpectSW: []uint16{0x6A82}}, nfc.SequenceStep{Data: selectApp})
	if res.StoppedAt != 0 || len(res.Replies) != 1 {
		t.Errorf("unmet expectSW: %d replies, stoppedAt %d, want 1 and 0", len(res.Replies), res.StoppedAt)
	}

	res = run(nfc.SequenceStep{Data: selectApp, ExpectSW: []uint16{0x9000}, StopOnSW: []uint16{0x9000}}, nfc.SequenceStep{Data: selectApp})
	if res.StoppedAt != 0 {
		t.Errorf("stopOnSW did not win over expectSW: stoppedAt %d", res.StoppedAt)
	}
}

func TestSequenceGating(t *testing.T) {
	steps := []nfc.SequenceStep{{Data: selectApp}}
	target := server.Target{AllowUntargeted: true}

	ro, _ := surfaceOps(t, nfc.ModeReadOnly, true)
	_, err := ro.TransceiveSequence(context.Background(), server.SequenceOp{Target: target, Steps: steps})
	if got := codeOf(err); got != protocol.ErrCodeReadOnly {
		t.Errorf("read-only: code = %q", got)
	}

	closed, _ := surfaceOps(t, nfc.ModeReadWrite, false)
	_, err = closed.TransceiveSequence(context.Background(), server.SequenceOp{Target: target, Steps: steps})
	if got := codeOf(err); got != protocol.ErrCodeRawChannelDisabled {
		t.Errorf("channel closed: code = %q", got)
	}

	open, _ := surfaceOps(t, nfc.ModeReadWrite, true)
	if _, err := open.TransceiveSequence(context.Background(), server.SequenceOp{Target: target}); err == nil {
		t.Error("an empty sequence was accepted")
	}
	tooMany := make([]nfc.SequenceStep, nfc.MaxSequenceSteps+1)
	for i := range tooMany {
		tooMany[i].Data = selectApp
	}
	if _, err := open.TransceiveSequence(context.Background(), server.SequenceOp{Target: target, Steps: tooMany}); err == nil {
		t.Error("an oversized sequence was accepted")
	}
}

func TestSequenceInsideASession(t *testing.T) {
	s, _ := surfaceOps(t, nfc.ModeReadWrite, true)
	ctx := context.Background()

	lease, err := s.BeginRawSession(ctx, server.RawSessionBeginOp{Target: server.Target{AllowUntargeted: true}})
	if err != nil {
		t.Fatalf("BeginRawSession: %v", err)
	}
	res, err := s.TransceiveSequence(ctx, server.SequenceOp{
		SessionID: lease.SessionID,
		Steps:     []nfc.SequenceStep{{Data: selectApp}, {Data: selectApp}},
	})
	if err != nil || len(res.Replies) != 2 {
		t.Fatalf("sequence in session = %+v, %v", res, err)
	}
	if err := s.EndRawSession(ctx, lease.SessionID); err != nil {
		t.Fatal(err)
	}

	_, err = s.TransceiveSequence(ctx, server.SequenceOp{SessionID: lease.SessionID, Steps: []nfc.SequenceStep{{Data: selectApp}}})
	if got := codeOf(err); got != protocol.ErrCodeRawSessionExpired {
		t.Errorf("ended session: code = %q, want %q", got, protocol.ErrCodeRawSessionExpired)
	}
}

type sequenceOps struct {
	stoppedOps
	mu   sync.Mutex
	seen []server.SequenceOp
	res  *nfc.SequenceResult
}

func (o *sequenceOps) TransceiveSequence(_ context.Context, req server.SequenceOp) (*nfc.SequenceResult, error) {
	o.mu.Lock()
	o.seen = append(o.seen, req)
	o.mu.Unlock()
	return o.res, nil
}

func TestTransceiveSequenceOverTheWire(t *testing.T) {
	ops := &sequenceOps{res: &nfc.SequenceResult{
		Replies:   [][]byte{{0x01, 0x90, 0x00}, {0x6A, 0x82}},
		StoppedAt: 1,
	}}
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	_ = conn.WriteJSON(map[string]any{
		"id":   "seq-1",
		"type": "transceiveSequenceRequest",
		"payload": map[string]any{
			"sessionId": "abc",
			"steps": []map[string]any{
				{"data": b64([]byte{0x00, 0xA4}), "expectSW": []string{"9000"}},
				{"data": b64([]byte{0x00, 0xB0}), "stopOnSW": []string{"6a82"}},
			},
		},
	})
	msg := readResponse(t, conn)
	if msg["type"] != "transceiveSequenceResponse" || msg["success"] != true {
		t.Fatalf("response = %v", msg)
	}
	payload := msg["payload"].(map[string]any)
	if payload["stoppedAt"] != float64(1) {
		t.Errorf("stoppedAt = %v", payload["stoppedAt"])
	}
	results := payload["results"].([]any)
	first := results[0].(map[string]any)
	if first["sw"] != "9000" || first["body"] != b64([]byte{0x01}) || first["data"] != b64([]byte{0x01, 0x90, 0x00}) {
		t.Errorf("first result = %v", first)
	}
	if second := results[1].(map[string]any); second["sw"] != "6A82" || second["body"] != nil {
		t.Errorf("second result = %v", second)
	}

	op := ops.seen[0]
	if op.SessionID != "abc" || len(op.Steps) != 2 || op.Steps[0].ExpectSW[0] != 0x9000 || op.Steps[1].StopOnSW[0] != 0x6A82 {
		t.Errorf("op = %+v", op)
	}
}

func TestTransceiveSequenceRejectsMalformedRequests(t *testing.T) {
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: &sequenceOps{res: &nfc.SequenceResult{StoppedAt: -1}}})
	conn := dial(t, s, "https://app.example.com")

	good := base64.StdEncoding.EncodeToString([]byte{0x00, 0xA4})
	many := make([]map[string]any, nfc.MaxSequenceSteps+1)
	for i := range many {
		many[i] = map[string]any{"data": good}
	}
	for name, steps := range map[string]any{
		"none":       []map[string]any{},
		"too many":   many,
		"bad base64": []map[string]any{{"data": "!!"}},
		"empty step": []map[string]any{{"data": ""}},
		"bad sw":     []map[string]any{{"data": good, "stopOnSW": []string{"90"}}},
	} {
		_ = conn.WriteJSON(map[string]any{
			"id": "x", "type": "transceiveSequenceRequest", "payload": map[string]any{"steps": steps},
		})
		msg := readResponse(t, conn)
		if msg["type"] != "error" {
			t.Errorf("%s: type = %v, want error", name, msg["type"])
		}
	}
}

func TestNTAG424OverTheWire(t *testing.T) {
	ops, _ := surfaceOps(t, nfc.ModeReadWrite, false)
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "https://app.example.com")

	_ = conn.WriteJSON(map[string]any{
		"id": "n-1", "type": "ntag424Request",
		"payload": map[string]any{"op": "getCardUID", "allowUntargeted": true},
	})
	msg := readResponse(t, conn)
	if msg["type"] != "ntag424Response" || msg["success"] != true {
		t.Fatalf("response = %v", msg)
	}
	payload := msg["payload"].(map[string]any)
	if payload["op"] != "getCardUID" || payload["uid"] != "04A1B2C3D4E5F6" {
		t.Errorf("payload = %v", payload)
	}

	_ = conn.WriteJSON(map[string]any{
		"id": "n-2", "type": "ntag424Request",
		"payload": map[string]any{"op": "lock", "allowUntargeted": true},
	})
	msg = readResponse(t, conn)
	if msg["type"] != "error" {
		t.Errorf("lock without confirm: type = %v, want error", msg["type"])
	}
}

// lockedBuffer collects log output from several loggers, and from server
// goroutines a test does not wait for, without racing.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureClientLogs(w io.Writer) func() {
	loggers := []*log.Logger{clientLog, clientWarn}
	flags := make([]int, len(loggers))
	outs := make([]io.Writer, len(loggers))
	for i, l := range loggers {
		flags[i], outs[i] = l.Flags(), l.Writer()
		l.SetOutput(w)
	}
	return func() {
		for i, l := range loggers {
			l.SetOutput(outs[i])
			l.SetFlags(flags[i])
		}
	}
}
