package scenario

import (
	"context"
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rawFrame is one frame of the issue #96 table and the reply length it
// expects. An empty wantLen means the frame must fail with a typed error.
type rawFrame struct {
	name    string
	frame   []byte
	wantLen int

	// failure is true for the frame that must fail.
	failure bool

	// soft marks a frame whose failure says something about the tag's
	// configuration rather than the framing: it is recorded and logged, not
	// failed.
	soft string
}

var rawFrames = []rawFrame{
	{name: "GET_VERSION", frame: []byte{0x60}, wantLen: 8},
	{name: "READ_SIG", frame: []byte{0x3C, 0x00}, wantLen: 32},
	{name: "READ_CNT", frame: []byte{0x39, 0x02}, wantLen: 3,
		soft: "an NTAG21x answers READ_CNT only when its NFC counter is enabled"},
	{name: "FAST_READ", frame: []byte{0x3A, 0x04, 0x07}, wantLen: 16},
	{name: "INVALID_PAGE", frame: []byte{0x30, 0xFF}, failure: true},
}

// RawConfig is what RunRawFraming needs.
type RawConfig struct {
	Sup     *nfc.Supervisor
	Rec     *fixture.Recorder
	Watcher *Watcher
	Tag     Tag
}

// RunRawFraming is the procedure of issue #96: it sends the frames of the
// issue's table through Supervisor.TransceiveTag with raw set, as the server
// does for a client's raw request, and checks each reply against what the issue
// expects. A reader that reports no raw support must refuse every frame as not
// supported and send the card nothing. It then reads the tag's NDEF the normal
// way to show the reader still works.
//
// The returned fixture holds what the reader saw for each frame.
func RunRawFraming(ctx context.Context, tb TB, cfg RawConfig) *fixture.Raw {
	tb.Helper()

	info, ok := cfg.Rec.Info()
	if !ok {
		tb.Fatalf("the recorder never saw a reader open; was the pcsc observer installed before the supervisor started?")
	}
	out := &fixture.Raw{
		Version:          fixture.Version,
		Kind:             fixture.KindRawFraming,
		Reader:           info.Name,
		ATR:              fixture.Hex(info.ATR),
		Method:           info.Method,
		CanTransceiveRaw: info.CanTransceiveRaw,
		Tag:              fixture.RawTag{UID: cfg.Tag.UID, Type: cfg.Tag.Card.Type},
		Setup:            capWires(cfg.Rec.Since(0)),
	}

	supported := info.CanTransceiveRaw
	for _, f := range rawFrames {
		mark := cfg.Rec.Mark()
		reply, err := cfg.Sup.TransceiveTag(ctx, cfg.Tag.Device, cfg.Tag.UID, f.frame, true)
		ex := fixture.RawExchange{
			Name:  f.name,
			Frame: fixture.Hex(f.frame),
			Wire:  cfg.Rec.Since(mark),
		}
		if err != nil {
			ex.Error = err.Error()
			ex.ErrorCode = errorCode(err)
		} else {
			ex.Reply = fixture.Hex(reply)
		}
		checkRaw(tb, f, supported, reply, err, &ex)
		out.Exchanges = append(out.Exchanges, ex)
	}

	out.NDEFAfter = readNDEF(tb, cfg.Tag.Card, cfg.Rec)
	return out
}

// maxSetupWires bounds the setup recording: a reader that polls for minutes
// before the test sends the card thousands of identical presence checks.
const maxSetupWires = 64

func capWires(w []fixture.Wire) []fixture.Wire {
	if len(w) > maxSetupWires {
		return w[:maxSetupWires]
	}
	return w
}

func errorCode(err error) string {
	var nerr *nfc.NFCError
	if errors.As(err, &nerr) {
		return fmt.Sprint(nerr.Code)
	}
	return ""
}

func checkRaw(tb TB, f rawFrame, supported bool, reply []byte, err error, ex *fixture.RawExchange) {
	tb.Helper()

	if !supported {
		if !nfc.IsNotSupportedError(err) {
			tb.Errorf("%s: a reader without raw support answered %v, want NOT_SUPPORTED", f.name, err)
		}
		if len(ex.Wire) != 0 {
			tb.Errorf("%s: a refused raw frame sent %d command(s) to the reader: %v", f.name, len(ex.Wire), ex.Wire)
		}
		return
	}

	var nerr *nfc.NFCError
	if f.failure {
		switch {
		case err == nil:
			tb.Errorf("%s: answered % X, want a typed error", f.name, reply)
		case !errors.As(err, &nerr):
			tb.Errorf("%s: error %v is not a typed *nfc.NFCError", f.name, err)
		default:
			tb.Logf("%s: failed as expected: %v", f.name, err)
		}
		return
	}

	switch {
	case err != nil && f.soft != "":
		ex.Note = f.soft
		tb.Logf("%s: failed (%v). Note: %s. The framing is not at fault if the reader answered the others", f.name, err, f.soft)
	case err != nil:
		tb.Errorf("%s: %v", f.name, err)
	case len(reply) != f.wantLen:
		tb.Errorf("%s: %d bytes % X, want %d", f.name, len(reply), reply, f.wantLen)
	default:
		tb.Logf("%s: % X", f.name, reply)
	}
}

// readNDEF reads the card normally, bypassing what the scan cached, and reports
// whether it worked. A tag with no NDEF message is a successful read of an
// empty tag.
func readNDEF(tb TB, card *nfc.Card, rec *fixture.Recorder) *fixture.NDEFCheck {
	tb.Helper()

	card.MessageData = nil
	card.Reset()
	msg, err := card.ReadMessage()
	switch {
	case err == nil:
		check := &fixture.NDEFCheck{Read: true}
		if ndef, ok := msg.(*nfc.NDEFMessage); ok {
			check.Records = len(ndef.Records())
		}
		tb.Logf("normal NDEF read after the raw frames: ok (%d record(s))", check.Records)
		return check
	case nfc.IsNoPayloadError(err):
		tb.Logf("normal NDEF read after the raw frames: ok, the tag holds no NDEF message")
		return &fixture.NDEFCheck{Read: true, Empty: true}
	default:
		tb.Errorf("normal NDEF read after the raw frames failed: %v", err)
		return &fixture.NDEFCheck{Error: err.Error()}
	}
}
