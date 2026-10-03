package nfctest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

const chainUID = "04A1B2C3D4E5F6"

var (
	getChunked = []byte{0x00, 0xCA, ChainP1Chunked, 0x00, 0x00}
	getShortLe = []byte{0x00, 0xCA, ChainP1ShortLe, 0x00, 0x00}
)

// appletCommands is what the card was sent by the tests: its own GET DATA and
// the GET RESPONSE that follows. The reader may probe a new ISO-DEP card to
// tell what it is, and a probe is not part of the exchange under test.
func appletCommands(card *ChainingApplet) [][]byte {
	var out [][]byte
	for _, cmd := range card.Commands() {
		if len(cmd) > 1 && (cmd[1] == 0xCA || cmd[1] == nfc.INSGetResponse) {
			out = append(out, cmd)
		}
	}
	return out
}

func chainingReader(t *testing.T) (*EmulatedReader, *ChainingApplet) {
	t.Helper()
	card := NewChainingApplet(chainUID)
	return NewEmulatedReader(t, card.EmulatedCard), card
}

func runSteps(t *testing.T, r *EmulatedReader, steps ...nfc.SequenceStep) *nfc.SequenceResult {
	t.Helper()
	res, err := r.TransceiveSequenceTag(context.Background(), readerPath, chainUID, steps)
	if err != nil {
		t.Fatalf("TransceiveSequenceTag: %v", err)
	}
	return res
}

func TestAutoGetResponse_CardAnswering61xxTwiceThen9000(t *testing.T) {
	r, card := chainingReader(t)

	res := runSteps(t, r, nfc.SequenceStep{Data: getChunked, AutoGetResponse: true})

	want := append(ChainChunked(), 0x90, 0x00)
	if len(res.Replies) != 1 || !bytes.Equal(res.Replies[0], want) {
		t.Fatalf("replies = % X, want one of % X", res.Replies, want)
	}

	getResponse := []byte{0x00, 0xC0, 0x00, 0x00, 0x10}
	sent := appletCommands(card)
	if len(sent) != 3 || !bytes.Equal(sent[0], getChunked) ||
		!bytes.Equal(sent[1], getResponse) || !bytes.Equal(sent[2], getResponse) {
		t.Errorf("card saw % X, want the command and two % X", sent, getResponse)
	}
	if len(res.Followups) != 1 || len(res.Followups[0]) != 2 {
		t.Errorf("follow-ups = % X, want two for the step", res.Followups)
	}
}

func TestAutoGetResponse_CardAnswering6C08(t *testing.T) {
	r, card := chainingReader(t)

	res := runSteps(t, r, nfc.SequenceStep{Data: getShortLe, AutoGetResponse: true})

	want := append(ChainShortLe(), 0x90, 0x00)
	if !bytes.Equal(res.Replies[0], want) {
		t.Fatalf("reply = % X, want % X", res.Replies[0], want)
	}
	retry := []byte{0x00, 0xCA, ChainP1ShortLe, 0x00, 0x08}
	sent := appletCommands(card)
	if len(sent) != 2 || !bytes.Equal(sent[0], getShortLe) || !bytes.Equal(sent[1], retry) {
		t.Errorf("card saw % X, want the command, then % X", sent, retry)
	}
}

// Without autoGetResponse the client gets what the card said, one round trip at
// a time, and the agent sends nothing the client did not ask for.
func TestAutoGetResponse_UnsetIsPurePassthrough(t *testing.T) {
	r, card := chainingReader(t)

	res := runSteps(t, r,
		nfc.SequenceStep{Data: getChunked},
		nfc.SequenceStep{Data: getShortLe},
	)

	if !bytes.Equal(res.Replies[0], []byte{0x61, 0x10}) {
		t.Errorf("chunked reply = % X, want 61 10 as the card sent it", res.Replies[0])
	}
	if !bytes.Equal(res.Replies[1], []byte{0x6C, 0x08}) {
		t.Errorf("short Le reply = % X, want 6C 08 as the card sent it", res.Replies[1])
	}
	if n := len(appletCommands(card)); n != 2 {
		t.Errorf("card saw %d commands, want the 2 sent: % X", n, appletCommands(card))
	}
	for i, f := range res.Followups {
		if len(f) != 0 {
			t.Errorf("step %d has follow-ups % X", i, f)
		}
	}
}

func TestAutoGetResponse_PerStepAndStopRulesSeeTheFinalStatus(t *testing.T) {
	r, card := chainingReader(t)

	res := runSteps(t, r,
		nfc.SequenceStep{Data: getChunked, AutoGetResponse: true, ExpectSW: []uint16{0x9000}},
		nfc.SequenceStep{Data: getShortLe, ExpectSW: []uint16{0x9000}},
		nfc.SequenceStep{Data: getShortLe},
	)

	if res.StoppedAt != 1 || len(res.Replies) != 2 {
		t.Fatalf("stoppedAt = %d with %d replies, want 1 and 2: the chained step met 9000 and the plain one answered 6C08", res.StoppedAt, len(res.Replies))
	}
	if n := len(appletCommands(card)); n != 4 {
		t.Errorf("card saw %d commands, want 4 (three for the chained step, one for the plain)", n)
	}
}

func TestAutoGetResponse_InsideARawSessionLease(t *testing.T) {
	r, card := chainingReader(t)
	ctx := context.Background()

	id, err := r.BeginRawSessionTag(ctx, readerPath, chainUID, 5*time.Second)
	if err != nil {
		t.Fatalf("BeginRawSessionTag: %v", err)
	}
	defer func() { _ = r.EndRawSessionTag(ctx, id) }()

	res, err := r.TransceiveSequenceInSessionTag(ctx, id, []nfc.SequenceStep{
		{Data: getChunked, AutoGetResponse: true},
		{Data: getShortLe, AutoGetResponse: true},
	})
	if err != nil {
		t.Fatalf("TransceiveSequenceInSessionTag: %v", err)
	}
	if !bytes.Equal(res.Replies[0], append(ChainChunked(), 0x90, 0x00)) ||
		!bytes.Equal(res.Replies[1], append(ChainShortLe(), 0x90, 0x00)) {
		t.Errorf("replies = % X", res.Replies)
	}
	if n := len(appletCommands(card)); n != 5 {
		t.Errorf("card saw %d commands, want 5", n)
	}
}
