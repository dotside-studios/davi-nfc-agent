package remotenfc

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

const simTagUID = "04A1B2C3D4E5F6"

var (
	simKey0 = bytes.Repeat([]byte{0x10}, 16)
	simKey1 = bytes.Repeat([]byte{0x11}, 16)
)

// simPhone is a phone holding an emulated NTAG 424 DNA. It answers the agent's
// device requests from the card, and counts the requests it was sent.
type simPhone struct {
	t        *testing.T
	conn     *websocket.Conn
	deviceID string
	card     *nfctest.EmulatedCard
	exchange nfc.TagTransceiver

	singles   atomic.Int32
	sequences atomic.Int32

	// sequenceHook, when set, rewrites the replies a sequence request is about
	// to be answered with, so a test can play a misbehaving device.
	sequenceHook func(resp *DeviceTransceiveSequenceResponse)

	// beforeReply, when set, runs after the card has answered a single exchange
	// and before the phone replies, so a test can make the phone slow by
	// advancing a clock.
	beforeReply func(cmd []byte)

	mu    sync.Mutex
	steps [][]DeviceSequenceStep
}

func simCard() *nfctest.EmulatedCard {
	return nfctest.NTAG424(simTagUID,
		nfctest.NTAG424WithKeys(map[byte][]byte{0: simKey0, 1: simKey1}),
	)
}

func simKeys() nfc.NTAG424Keys {
	return nfc.NTAG424Keys{Slots: map[byte][]byte{0: simKey0, 1: simKey1}}
}

// newSimPhone registers a phone declaring caps, has it report the card, and
// serves the agent's requests until the test ends.
func newSimPhone(t *testing.T, m *Manager, url string, caps *DeviceCapabilities, tagType string) *simPhone {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	hello := map[string]any{
		"protocolVersion": DeviceProtocolV1,
		"deviceName":      "Sim Phone",
		"platform":        "android",
	}
	if caps != nil {
		hello["capabilities"] = caps
	}
	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: WSTypeHello, Payload: hello}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp struct {
		Payload struct {
			DeviceID string `json:"deviceID"`
		} `json:"payload"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read hello response: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	card := simCard()
	p := &simPhone{t: t, conn: conn, deviceID: resp.Payload.DeviceID, card: card, exchange: card.Tag().(nfc.TagTransceiver)}

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   p.deviceID,
			"uid":        simTagUID,
			"technology": "ISO14443A",
			"type":       tagType,
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := m.ActiveTag(p.deviceID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never recorded the phone's tag")
		}
		time.Sleep(5 * time.Millisecond)
	}

	go p.serve()
	return p
}

func (p *simPhone) serve() {
	for {
		var req protocol.WebSocketRequest
		if err := p.conn.ReadJSON(&req); err != nil {
			return
		}
		switch req.Type {
		case WSTypeDeviceTransceiveRequest:
			p.singles.Add(1)
			var in DeviceTransceiveRequest
			if err := decodePayload(req.Payload, &in); err != nil {
				p.t.Errorf("decode transceive: %v", err)
				return
			}
			out := DeviceTransceiveResponse{RequestID: in.RequestID, Success: true}
			reply, err := p.exchange.Transceive(in.Data)
			if err != nil {
				out.Success, out.Error = false, err.Error()
			}
			out.Data = reply
			if p.beforeReply != nil {
				p.beforeReply(in.Data)
			}
			p.send(WSTypeDeviceTransceiveResponse, out)

		case WSTypeDeviceTransceiveSequenceRequest:
			p.sequences.Add(1)
			var in DeviceTransceiveSequenceRequest
			if err := decodePayload(req.Payload, &in); err != nil {
				p.t.Errorf("decode sequence: %v", err)
				return
			}
			p.mu.Lock()
			p.steps = append(p.steps, in.Steps)
			p.mu.Unlock()
			p.send(WSTypeDeviceTransceiveSequenceResponse, p.runSequence(in))
		}
	}
}

// runSequence is the phone's own loop, written independently of the agent's:
// parse the status words, send each command, stop as the rules say.
func (p *simPhone) runSequence(in DeviceTransceiveSequenceRequest) DeviceTransceiveSequenceResponse {
	out := DeviceTransceiveSequenceResponse{RequestID: in.RequestID, Success: true, StoppedAt: -1}
	for i, step := range in.Steps {
		reply, err := p.exchange.Transceive(step.Data)
		if err != nil {
			return DeviceTransceiveSequenceResponse{RequestID: in.RequestID, Error: err.Error(), ErrorCode: protocol.ErrCodeTransceiveFailed}
		}
		out.Replies = append(out.Replies, reply)

		sw := strings.ToUpper(hexOf(reply[len(reply)-2:]))
		stop := false
		for _, s := range step.StopOnSW {
			stop = stop || s == sw
		}
		if len(step.ExpectSW) > 0 {
			ok := false
			for _, s := range step.ExpectSW {
				ok = ok || s == sw
			}
			stop = stop || !ok
		}
		if stop {
			out.StoppedAt = i
			break
		}
	}
	if p.sequenceHook != nil {
		p.sequenceHook(&out)
	}
	return out
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xF])
	}
	return string(out)
}

func (p *simPhone) send(msgType string, payload any) {
	if err := p.conn.WriteJSON(protocol.WebSocketResponse{Type: msgType, Payload: payload}); err != nil {
		p.t.Logf("sim phone could not answer: %v", err)
	}
}

func simSupervisor(t *testing.T, m *Manager, mode nfc.ReaderMode) *nfc.Supervisor {
	t.Helper()
	s, err := nfc.NewSupervisor(m, time.Second)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	s.SetMode(mode)
	s.SetNTAG424Keys(simKeys())
	return s
}

var (
	selectNDEFApp = []byte{0x00, 0xA4, 0x04, 0x00, 0x07, 0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00}
	badCommand    = []byte{0x00, 0xB0, 0x00, 0x00, 0x00}
)

func sequenceCaps() *DeviceCapabilities {
	return &DeviceCapabilities{CanRead: true, CanWrite: true, NFCType: "isodep", CanTransceive: true, CanTransceiveSequence: true, CanLock: true}
}

func exchangeCaps() *DeviceCapabilities {
	return &DeviceCapabilities{CanRead: true, CanWrite: true, NFCType: "isodep", CanTransceive: true, CanLock: true}
}

// A phone that declares canTransceiveSequence runs a batch in one request, and
// the agent holds it to the stop rules it sent.
func TestSimPhone_SequenceInOneRequest(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	res, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{
		{Data: selectNDEFApp, ExpectSW: []uint16{0x9000}},
		{Data: badCommand, ExpectSW: []uint16{0x9000}},
		{Data: selectNDEFApp},
	})
	if err != nil {
		t.Fatalf("TransceiveSequenceTag: %v", err)
	}
	if phone.sequences.Load() != 1 || phone.singles.Load() != 0 {
		t.Errorf("round trips: %d sequence and %d single requests, want 1 and 0", phone.sequences.Load(), phone.singles.Load())
	}
	if res.StoppedAt != 1 || len(res.Replies) != 2 {
		t.Errorf("StoppedAt = %d with %d replies, want 1 with 2: an unexpected status word ends the run", res.StoppedAt, len(res.Replies))
	}

	phone.mu.Lock()
	defer phone.mu.Unlock()
	if got := phone.steps[0][0].ExpectSW; len(got) != 1 || got[0] != "9000" {
		t.Errorf("expectSW on the wire = %v, want [9000]", got)
	}
}

func TestSimPhone_SequenceStopOnSW(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	res, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{
		{Data: selectNDEFApp, StopOnSW: []uint16{0x9000}},
		{Data: selectNDEFApp},
	})
	if err != nil {
		t.Fatalf("TransceiveSequenceTag: %v", err)
	}
	if res.StoppedAt != 0 || len(res.Replies) != 1 {
		t.Errorf("StoppedAt = %d with %d replies, want 0 with 1", res.StoppedAt, len(res.Replies))
	}
}

// A phone that did not declare it is sent one exchange per step, under the same
// stop rules, and the caller cannot tell the difference.
func TestSimPhone_SequenceFallsBackToOneExchangePerStep(t *testing.T) {
	for name, caps := range map[string]*DeviceCapabilities{
		"declared canTransceive only": exchangeCaps(),
		"declared nothing":            nil,
	} {
		t.Run(name, func(t *testing.T) {
			m, url := serveManager(t, time.Minute)
			phone := newSimPhone(t, m, url, caps, "Type4")
			s := simSupervisor(t, m, nfc.ModeReadWrite)

			res, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{
				{Data: selectNDEFApp, ExpectSW: []uint16{0x9000}},
				{Data: badCommand, ExpectSW: []uint16{0x9000}},
				{Data: selectNDEFApp},
			})
			if err != nil {
				t.Fatalf("TransceiveSequenceTag: %v", err)
			}
			if phone.sequences.Load() != 0 || phone.singles.Load() != 2 {
				t.Errorf("round trips: %d sequence and %d single requests, want 0 and 2", phone.sequences.Load(), phone.singles.Load())
			}
			if res.StoppedAt != 1 || len(res.Replies) != 2 {
				t.Errorf("StoppedAt = %d with %d replies, want 1 with 2", res.StoppedAt, len(res.Replies))
			}
		})
	}
}

// The device sequence message has no autoGetResponse, so a run that asks for it
// is driven step by step even on a phone that declared sequences.
func TestSimPhone_ChainedSequenceRunsStepByStep(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	res, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{
		{Data: selectNDEFApp, AutoGetResponse: true},
		{Data: selectNDEFApp},
	})
	if err != nil {
		t.Fatalf("TransceiveSequenceTag: %v", err)
	}
	if phone.sequences.Load() != 0 || phone.singles.Load() != 2 {
		t.Errorf("round trips: %d sequence and %d single requests, want 0 and 2", phone.sequences.Load(), phone.singles.Load())
	}
	if res.StoppedAt != -1 || len(res.Replies) != 2 {
		t.Errorf("StoppedAt = %d with %d replies, want -1 with 2", res.StoppedAt, len(res.Replies))
	}
}

// A device is not believed over the rules the agent sent it.
func TestSimPhone_InconsistentSequenceAnswerIsRefused(t *testing.T) {
	cases := map[string]func(*DeviceTransceiveSequenceResponse){
		"ran past a stop": func(r *DeviceTransceiveSequenceResponse) {
			r.Replies = append(r.Replies, r.Replies[0])
		},
		"stoppedAt disagrees": func(r *DeviceTransceiveSequenceResponse) { r.StoppedAt = 0 },
		"stopped short":       func(r *DeviceTransceiveSequenceResponse) { r.Replies = r.Replies[:1]; r.StoppedAt = -1 },
		"no replies":          func(r *DeviceTransceiveSequenceResponse) { r.Replies = nil },
	}
	for name, hook := range cases {
		t.Run(name, func(t *testing.T) {
			m, url := serveManager(t, time.Minute)
			phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
			phone.sequenceHook = hook
			s := simSupervisor(t, m, nfc.ModeReadWrite)

			_, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{
				{Data: selectNDEFApp, ExpectSW: []uint16{0x9000}},
				{Data: badCommand, ExpectSW: []uint16{0x9000}},
				{Data: selectNDEFApp},
			})
			if err == nil || !strings.Contains(err.Error(), "inconsistently") {
				t.Fatalf("err = %v, want a refusal of the inconsistent answer", err)
			}
		})
	}
}

func TestSimPhone_SequenceRefusals(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, &DeviceCapabilities{CanRead: true, NFCType: "isodep"}, "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	_, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{{Data: selectNDEFApp}})
	if !nfc.IsNotSupportedError(err) {
		t.Errorf("a phone that declared no exchange: err = %v, want not supported", err)
	}

	m2, url2 := serveManager(t, time.Minute)
	ok := newSimPhone(t, m2, url2, sequenceCaps(), "Type4")
	s2 := simSupervisor(t, m2, nfc.ModeReadWrite)
	if _, err := s2.TransceiveSequenceTag(context.Background(), ok.deviceID, "04FFFFFF", []nfc.SequenceStep{{Data: selectNDEFApp}}); err == nil {
		t.Error("a sequence for a tag the phone is not holding was sent")
	}
	if _, err := s2.TransceiveSequenceTag(context.Background(), ok.deviceID, simTagUID, nil); err == nil {
		t.Error("an empty sequence was sent")
	}
	if ok.sequences.Load()+ok.singles.Load() != 0 {
		t.Error("a refused sequence reached the phone")
	}
}

// A phone's failure on a step reaches the caller as the failure it reported.
func TestSimPhone_SequenceFailureKeepsDeviceCode(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
	phone.sequenceHook = func(r *DeviceTransceiveSequenceResponse) {
		*r = DeviceTransceiveSequenceResponse{RequestID: r.RequestID, Error: "tag lost", ErrorCode: protocol.ErrCodeTagRemoved}
	}
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	_, err := s.TransceiveSequenceTag(context.Background(), phone.deviceID, simTagUID, []nfc.SequenceStep{{Data: selectNDEFApp}})
	if err == nil || !strings.Contains(err.Error(), "tag lost") {
		t.Fatalf("err = %v, want the device's message", err)
	}
	if got := protocol.ErrorPayloadFor(err).Code; got != protocol.ErrCodeTagRemoved {
		t.Errorf("code = %s, want TAG_REMOVED", got)
	}
}

func withSimNTAG424(t *testing.T, s *nfc.Supervisor, phone *simPhone, fn func(nfc.NTAG424Operator) error) error {
	t.Helper()
	return s.WithNTAG424Tag(context.Background(), phone.deviceID, simTagUID, fn)
}

// The driver a reader uses runs over a phone's exchange: EV2 authentication
// costs two device round trips and each protected command one more.
func TestSimPhone_NTAG424OperationsOverDevice(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	err := withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
		fs, err := op.GetFileSettings(ntag424.NDEFFileNo)
		if err != nil {
			return err
		}
		if fs.FileSize != 256 {
			t.Errorf("NDEF file size = %d, want 256", fs.FileSize)
		}

		uid, err := op.GetCardUID()
		if err != nil {
			return err
		}
		if got := nfc.BytesToHex(uid); got != simTagUID {
			t.Errorf("card UID = %s, want %s", got, simTagUID)
		}

		v, err := op.GetKeyVersion(0)
		if err != nil {
			return err
		}
		if v != 0 {
			t.Errorf("key 0 version = %d, want 0", v)
		}

		sig, err := op.ReadSig()
		if err != nil {
			return err
		}
		if len(sig) != 56 {
			t.Errorf("signature is %d bytes, want 56", len(sig))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NTAG 424 operations over a phone: %v", err)
	}
	if phone.sequences.Load() != 0 {
		t.Error("the driver sent a sequence request to a phone that did not declare them")
	}
	if !phone.card.NTAG424Authenticated() {
		t.Error("no EV2 session was opened on the card")
	}
}

// EV2 authentication is two exchanges, AuthenticateEV2First and its second
// part, on top of the application select that precedes it and the
// command itself.
func TestSimPhone_NTAG424AuthenticationIsTwoRoundTrips(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	err := withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
		_, err := op.ReadSig()
		return err
	})
	if err != nil {
		t.Fatalf("ReadSig: %v", err)
	}
	const selectApp, authenticate, command = 1, 2, 1
	if got := phone.singles.Load(); got != selectApp+authenticate+command {
		t.Errorf("a first protected command took %d device exchanges, want %d (select, two for authentication, the command)",
			got, selectApp+authenticate+command)
	}
}

func TestSimPhone_NTAG424ConfigureSDMAndChangeKey(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	plan, err := ntag424.PlanSDM("https://davi.social/t?picc={picc}&mac={mac}", ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: ntag424.AccessNever, Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0,
	})
	if err != nil {
		t.Fatalf("PlanSDM: %v", err)
	}

	err = withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
		res, err := op.ConfigureSDM(plan)
		if err != nil {
			return err
		}
		if res.Tap == nil || !strings.HasPrefix(res.URL, "https://davi.social/t?picc=") {
			t.Errorf("SDM read-back = %+v, want a verified tap of the mirrored URL", res)
		}
		return op.ChangeKey(1, bytes.Repeat([]byte{0x22}, 16), 1, 0)
	})
	if err != nil {
		t.Fatalf("configure SDM and change key over a phone: %v", err)
	}

	err = withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
		v, err := op.GetKeyVersion(1)
		if err == nil && v != 1 {
			t.Errorf("key 1 version = %d, want 1", v)
		}
		return err
	})
	if err != nil {
		t.Fatalf("GetKeyVersion after ChangeKey: %v", err)
	}
}

func TestSimPhone_NTAG424ReadsAllowedInReadOnlyMode(t *testing.T) {
	m := NewManager(time.Minute)
	url := newDeviceServer(t, m, ServerOptions{AllowTagModification: func() bool { return false }})
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadOnly)

	err := withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
		_, err := op.GetCardUID()
		return err
	})
	if err != nil {
		t.Fatalf("a read in read-only mode: %v", err)
	}
}

func TestSimPhone_NTAG424Refusals(t *testing.T) {
	t.Run("another card family", func(t *testing.T) {
		m, url := serveManager(t, time.Minute)
		phone := newSimPhone(t, m, url, exchangeCaps(), "MIFARE Classic 1K")
		s := simSupervisor(t, m, nfc.ModeReadWrite)

		err := withSimNTAG424(t, s, phone, func(nfc.NTAG424Operator) error { return nil })
		if !nfc.IsNotSupportedError(err) {
			t.Errorf("err = %v, want not supported", err)
		}
		if phone.singles.Load() != 0 {
			t.Error("a command was sent to a card that is not an NTAG 424 DNA")
		}
	})
	t.Run("device declared no exchange", func(t *testing.T) {
		m, url := serveManager(t, time.Minute)
		phone := newSimPhone(t, m, url, &DeviceCapabilities{CanRead: true}, "Type4")
		s := simSupervisor(t, m, nfc.ModeReadWrite)

		err := withSimNTAG424(t, s, phone, func(nfc.NTAG424Operator) error { return nil })
		if !nfc.IsNotSupportedError(err) {
			t.Errorf("err = %v, want not supported", err)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		m, url := serveManager(t, time.Minute)
		phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
		s := simSupervisor(t, m, nfc.ModeReadWrite)
		s.SetNTAG424Keys(nfc.NTAG424Keys{Slots: map[byte][]byte{0: bytes.Repeat([]byte{0x99}, 16)}})

		err := withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
			_, err := op.ReadSig()
			return err
		})
		if !nfc.IsAuthError(err) {
			t.Errorf("err = %v, want a refused authentication", err)
		}
	})
}

// Two operations on one phone do not interleave their exchanges on the tag.
func TestSimPhone_OperationsOnOneDeviceAreSerialized(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	var inside, overlapped atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withSimNTAG424(t, s, phone, func(op nfc.NTAG424Operator) error {
				if inside.Add(1) > 1 {
					overlapped.Add(1)
				}
				defer inside.Add(-1)
				_, err := op.GetCardUID()
				return err
			})
			if err != nil {
				t.Errorf("operation: %v", err)
			}
		}()
	}
	wg.Wait()
	if overlapped.Load() != 0 {
		t.Error("two operations ran on one device at once")
	}
}

// A caller that gives up while another operation holds the device is released.
func TestSimPhone_WaitingForTheDeviceHonoursContext(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = withSimNTAG424(t, s, phone, func(nfc.NTAG424Operator) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := s.WithNTAG424Tag(ctx, phone.deviceID, simTagUID, func(nfc.NTAG424Operator) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context's deadline", err)
	}
}

// A lease would hold the reader for a client, and a phone is not one: its
// operating system owns the tag session.
func TestSimPhone_RawSessionIsNotSupported(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, sequenceCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	_, err := s.BeginRawSessionTag(context.Background(), phone.deviceID, simTagUID, time.Second)
	if !nfc.IsNotSupportedError(err) {
		t.Fatalf("err = %v, want not supported", err)
	}
	if !strings.Contains(err.Error(), "operating system owns the tag session") {
		t.Errorf("err = %q, want the reason", err)
	}
}
