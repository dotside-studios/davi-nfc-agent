package remotenfc

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// A phone's latency here is simulated time: the phone advances a fake clock by
// what each exchange takes, and the agent measures its deadline on that clock,
// so no test waits for it.

const simSDMTemplate = "https://davi.social/t?picc={picc}&mac={mac}"

func simSDMPlan(t *testing.T) *ntag424.SDMPlan {
	t.Helper()
	plan, err := ntag424.PlanSDM(simSDMTemplate, ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: ntag424.AccessNever, Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0,
	})
	if err != nil {
		t.Fatalf("PlanSDM: %v", err)
	}
	return plan
}

// slowPhone is a phone whose every exchange takes latency(cmd) of the clock's
// time, and which counts the commands that change the card it was sent.
type slowPhone struct {
	*simPhone
	clock  *nfc.FakeClock
	writes atomic.Int32
}

func newSlowPhone(t *testing.T, latency func(cmd []byte) time.Duration) (*slowPhone, *nfc.Supervisor) {
	t.Helper()
	m, url := serveManager(t, time.Minute)
	phone := newSimPhone(t, m, url, exchangeCaps(), "Type4")
	s := simSupervisor(t, m, nfc.ModeReadWrite)

	sp := &slowPhone{simPhone: phone, clock: nfc.NewFakeClock(time.Now())}
	s.SetClock(sp.clock)
	phone.beforeReply = func(cmd []byte) {
		if isCardChange(cmd) {
			sp.writes.Add(1)
		}
		sp.clock.Advance(latency(cmd))
	}
	return sp, s
}

// isCardChange reports whether an APDU changes the card: an ISO UPDATE BINARY, or
// a native WriteData, ChangeFileSettings or ChangeKey.
func isCardChange(cmd []byte) bool {
	if len(cmd) < 2 {
		return false
	}
	switch {
	case cmd[0] == 0x00 && cmd[1] == 0xD6:
		return true
	case cmd[0] == 0x90 && (cmd[1] == 0x8D || cmd[1] == 0x5F || cmd[1] == 0xC4):
		return true
	}
	return false
}

func constantLatency(d time.Duration) func([]byte) time.Duration {
	return func([]byte) time.Duration { return d }
}

type cardImage struct {
	data     []byte
	settings ntag424.FileSettings
}

func imageOf(p *simPhone) cardImage {
	return cardImage{data: p.card.NTAG424FileData(ntag424.NDEFFileNo), settings: p.card.NTAG424FileSettings(ntag424.NDEFFileNo)}
}

func configureSDMOn(t *testing.T, s *nfc.Supervisor, ctx context.Context, p *simPhone) (*nfc.NTAG424SDMResult, error) {
	t.Helper()
	var res *nfc.NTAG424SDMResult
	err := s.WithNTAG424Tag(ctx, p.deviceID, simTagUID, func(op nfc.NTAG424Operator) error {
		var err error
		res, err = op.ConfigureSDM(simSDMPlan(t))
		return err
	})
	return res, err
}

func wantSDMState(t *testing.T, err error, want nfc.SDMState) {
	t.Helper()
	got, ok := nfc.SDMStateOf(err)
	if !ok || got != want {
		t.Fatalf("tag state in %v = %q (reported: %v), want %q", err, got, ok, want)
	}
}

// A phone too slow for the operation is refused before the first write: the
// error is the typed deadline error, it says the tag is untouched, and the
// card is exactly as it was.
func TestSimPhone_SlowPhoneFailsBeforeFirstWrite(t *testing.T) {
	phone, s := newSlowPhone(t, constantLatency(3*time.Second))
	before := imageOf(phone.simPhone)

	_, err := configureSDMOn(t, s, context.Background(), phone.simPhone)
	if !nfc.IsOperationDeadlineError(err) {
		t.Fatalf("err = %v, want an operation deadline error", err)
	}
	wantSDMState(t, err, nfc.SDMNothingWritten)

	var dl *nfc.OperationDeadlineError
	if !errors.As(err, &dl) || dl.Step != "write NDEF data" || dl.PerExchange != 3*time.Second || dl.Remaining <= 0 {
		t.Errorf("deadline error = %+v, want the NDEF write, estimated at the 3s the phone took", dl)
	}
	if phone.writes.Load() != 0 {
		t.Errorf("%d commands that change the card were sent", phone.writes.Load())
	}
	if after := imageOf(phone.simPhone); !reflect.DeepEqual(before, after) {
		t.Error("the emulated card changed")
	}
}

// A phone that stalls after the NDEF write leaves the tag with the message
// written and SDM not configured, and the error says exactly that.
func TestSimPhone_StallBetweenNDEFWriteAndSettingsChange(t *testing.T) {
	var lengths atomic.Int32
	phone, s := newSlowPhone(t, func(cmd []byte) time.Duration {
		// The third UPDATE BINARY is the final NLEN, the last step of the
		// NDEF write.
		if len(cmd) > 1 && cmd[0] == 0x00 && cmd[1] == 0xD6 && lengths.Add(1) == 3 {
			return 17 * time.Second
		}
		return 100 * time.Millisecond
	})
	before := imageOf(phone.simPhone)

	_, err := configureSDMOn(t, s, context.Background(), phone.simPhone)
	if !nfc.IsOperationDeadlineError(err) {
		t.Fatalf("err = %v, want an operation deadline error", err)
	}
	wantSDMState(t, err, nfc.SDMNDEFWritten)

	var dl *nfc.OperationDeadlineError
	if !errors.As(err, &dl) || dl.Step != "change file settings" {
		t.Errorf("deadline error = %+v, want the settings change", dl)
	}

	after := imageOf(phone.simPhone)
	if bytes.Equal(before.data, after.data) {
		t.Error("the NDEF message was not written, so the state reported is not the card's")
	}
	if after.settings.SDMEnabled || !reflect.DeepEqual(before.settings, after.settings) {
		t.Errorf("file settings = %+v, want them unchanged from %+v", after.settings, before.settings)
	}
	if phone.writes.Load() != 3 {
		t.Errorf("%d commands that change the card were sent, want the 3 of the NDEF write", phone.writes.Load())
	}
}

// Running ConfigureSDM again repairs the half-configured tag, and running it on
// a tag it already configured leaves it as it was: it is idempotent.
func TestSimPhone_ConfigureSDMIsIdempotent(t *testing.T) {
	var stall atomic.Bool
	stall.Store(true)
	var lengths atomic.Int32
	phone, s := newSlowPhone(t, func(cmd []byte) time.Duration {
		if stall.Load() && len(cmd) > 1 && cmd[0] == 0x00 && cmd[1] == 0xD6 && lengths.Add(1) == 3 {
			return 17 * time.Second
		}
		return 50 * time.Millisecond
	})

	if _, err := configureSDMOn(t, s, context.Background(), phone.simPhone); !nfc.IsOperationDeadlineError(err) {
		t.Fatalf("first run: err = %v, want the stall to fail it", err)
	}
	stall.Store(false)

	res, err := configureSDMOn(t, s, context.Background(), phone.simPhone)
	if err != nil || res.Tap == nil {
		t.Fatalf("run after the failure: %v, %+v, want a verified configuration", err, res)
	}
	configured := imageOf(phone.simPhone)
	if !configured.settings.SDMEnabled {
		t.Fatal("SDM is not enabled after the repair")
	}

	res, err = configureSDMOn(t, s, context.Background(), phone.simPhone)
	if err != nil || res.Tap == nil {
		t.Fatalf("run on a configured tag: %v, %+v", err, res)
	}
	if again := imageOf(phone.simPhone); !reflect.DeepEqual(configured, again) {
		t.Error("a repeated run changed the tag")
	}
}

func TestSimPhone_FastPhoneConfiguresSDM(t *testing.T) {
	phone, s := newSlowPhone(t, constantLatency(80*time.Millisecond))

	res, err := configureSDMOn(t, s, context.Background(), phone.simPhone)
	if err != nil {
		t.Fatalf("ConfigureSDM: %v", err)
	}
	if res.Tap == nil || !phone.card.NTAG424FileSettings(ntag424.NDEFFileNo).SDMEnabled {
		t.Errorf("result = %+v, want a verified tap on a tag with SDM on", res)
	}
}

// A caller that gives less time than the default is held to it: the operation
// fails before writing rather than using the default's.
func TestSimPhone_ShorterCallerContextIsHonoured(t *testing.T) {
	phone, s := newSlowPhone(t, constantLatency(50*time.Millisecond))
	before := imageOf(phone.simPhone)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := configureSDMOn(t, s, ctx, phone.simPhone)

	var dl *nfc.OperationDeadlineError
	if !errors.As(err, &dl) {
		t.Fatalf("err = %v, want an operation deadline error", err)
	}
	wantSDMState(t, err, nfc.SDMNothingWritten)
	if dl.Remaining >= time.Second {
		t.Errorf("remaining = %s, want about the caller's 500ms and not the default's %s", dl.Remaining, nfc.NTAG424PhoneOperationTimeout)
	}
	if phone.writes.Load() != 0 || !reflect.DeepEqual(before, imageOf(phone.simPhone)) {
		t.Error("the card was written")
	}
}

// The operation's own time can be set below the default, and is the budget the
// steps are checked against.
func TestSimPhone_OperationTimeoutIsConfigurable(t *testing.T) {
	phone, s := newSlowPhone(t, constantLatency(50*time.Millisecond))
	s.SetPhoneOperationTimeout(time.Second)

	_, err := configureSDMOn(t, s, context.Background(), phone.simPhone)
	if !nfc.IsOperationDeadlineError(err) {
		t.Fatalf("err = %v, want an operation deadline error", err)
	}

	s.SetPhoneOperationTimeout(0)
	if _, err := configureSDMOn(t, s, context.Background(), phone.simPhone); err != nil {
		t.Fatalf("with the default timeout restored: %v", err)
	}
}

// A key change is refused too when the phone is too slow for it, with nothing
// sent.
func TestSimPhone_SlowPhoneChangeKeyFailsBeforeSending(t *testing.T) {
	phone, s := newSlowPhone(t, constantLatency(4*time.Second))
	before := phone.card.NTAG424KeyVersion(1)

	err := s.WithNTAG424Tag(context.Background(), phone.deviceID, simTagUID, func(op nfc.NTAG424Operator) error {
		// A read first, so the budget has seen the phone's pace.
		if _, err := op.GetKeyVersion(0); err != nil {
			return err
		}
		if _, err := op.GetCardUID(); err != nil {
			return err
		}
		if _, err := op.ReadSig(); err != nil {
			return err
		}
		return op.ChangeKey(1, bytes.Repeat([]byte{0x22}, 16), 1, 0)
	})
	if !nfc.IsOperationDeadlineError(err) {
		t.Fatalf("err = %v, want an operation deadline error", err)
	}
	if phone.writes.Load() != 0 || phone.card.NTAG424KeyVersion(1) != before {
		t.Error("a key change reached the card")
	}
}
