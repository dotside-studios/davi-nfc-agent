package nfc_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

func TestOpBudgetRequire(t *testing.T) {
	clock := nfc.NewFakeClock(time.Unix(1000, 0))
	b := nfc.NewOpBudget(clock, clock.Now().Add(10*time.Second), 100*time.Millisecond)

	if err := b.Require("step", 100); err != nil {
		t.Errorf("100 exchanges at the 100ms floor fit in 10s: %v", err)
	}
	err := b.Require("step", 101)
	var dl *nfc.OperationDeadlineError
	if !errors.As(err, &dl) {
		t.Fatalf("101 exchanges: err = %v, want a deadline error", err)
	}
	if dl.Step != "step" || dl.Exchanges != 101 || dl.PerExchange != 100*time.Millisecond || dl.Remaining != 10*time.Second {
		t.Errorf("error = %+v", dl)
	}
	if !strings.Contains(err.Error(), "deadline would be exceeded before step") || !strings.Contains(err.Error(), "no write sent") {
		t.Errorf("message = %q", err)
	}

	// The slowest exchange seen sets the estimate, and a faster one does not
	// lower it.
	b.Observe(500 * time.Millisecond)
	b.Observe(50 * time.Millisecond)
	if got := b.PerExchange(); got != 500*time.Millisecond {
		t.Errorf("estimate = %s, want the slowest seen, 500ms", got)
	}
	if err := b.Require("step", 21); err == nil {
		t.Error("21 exchanges at 500ms fit in 10s")
	}

	clock.Advance(9 * time.Second)
	if got := b.Remaining(); got != time.Second {
		t.Errorf("remaining = %s, want 1s", got)
	}
	if err := b.Require("step", 2); err != nil {
		t.Errorf("2 exchanges at 500ms fit in 1s: %v", err)
	}
	if err := b.Require("step", 3); err == nil {
		t.Error("3 exchanges at 500ms fit in 1s")
	}
}

func TestOpBudgetNilHasNoLimit(t *testing.T) {
	var b *nfc.OpBudget
	b.Observe(time.Hour)
	if err := b.Require("step", 1<<20); err != nil {
		t.Errorf("a nil budget refused: %v", err)
	}
}

func TestOpBudgetTimesExchanges(t *testing.T) {
	clock := nfc.NewFakeClock(time.Unix(1000, 0))
	b := nfc.NewOpBudget(clock, clock.Now().Add(time.Minute), time.Millisecond)
	transport := b.Wrap(&advancingTransport{clock: clock, per: 2 * time.Second})

	if _, err := transport.Transceive([]byte{0x00}); err != nil {
		t.Fatal(err)
	}
	if got := b.PerExchange(); got != 2*time.Second {
		t.Errorf("estimate = %s, want the 2s the exchange took", got)
	}
}

type advancingTransport struct {
	clock *nfc.FakeClock
	per   time.Duration
}

func (a *advancingTransport) Transceive([]byte) ([]byte, error) {
	a.clock.Advance(a.per)
	return []byte{0x90, 0x00}, nil
}
func (a *advancingTransport) IsCardPresent() bool { return true }

// faultyTransport passes exchanges to a card and fails the ones the test
// names. With dropReply the card has executed the exchange and only its answer
// is lost.
type faultyTransport struct {
	nfc.CardTransport

	failAt    func(n int, cmd []byte) bool
	dropReply bool

	n int
}

func (f *faultyTransport) Transceive(cmd []byte) ([]byte, error) {
	f.n++
	fail := f.failAt(f.n, cmd)
	if fail && !f.dropReply {
		return nil, errors.New("link lost")
	}
	resp, err := f.CardTransport.Transceive(cmd)
	if err == nil && fail {
		return nil, errors.New("link lost")
	}
	return resp, err
}

func isUpdateBinary(cmd []byte) bool   { return len(cmd) > 1 && cmd[0] == 0x00 && cmd[1] == 0xD6 }
func isChangeSettings(cmd []byte) bool { return len(cmd) > 1 && cmd[0] == 0x90 && cmd[1] == 0x5F }

func sdmTestPlan(t *testing.T) *ntag424.SDMPlan {
	t.Helper()
	plan, err := ntag424.PlanSDM("https://davi.social/t?picc={picc}&mac={mac}", ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: ntag424.AccessNever, Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0,
	})
	if err != nil {
		t.Fatalf("PlanSDM: %v", err)
	}
	return plan
}

func sdmTestCard() *nfctest.EmulatedCard {
	return nfctest.NTAG424("04A1B2C3D4E5F6", nfctest.NTAG424WithKeys(map[byte][]byte{0: bytes.Repeat([]byte{0x10}, 16)}))
}

func sdmTestKeys() nfc.NTAG424Keys {
	return nfc.NTAG424Keys{Slots: map[byte][]byte{0: bytes.Repeat([]byte{0x10}, 16)}}
}

// ConfigureSDM says what state it left the tag in for each place it can fail,
// and running it again with the same plan repairs each: it is idempotent.
func TestConfigureSDMReportsTheStateItLeft(t *testing.T) {
	plan := sdmTestPlan(t)

	reference := sdmTestCard()
	if _, err := nfc.NewNTAG424Session(reference.Transport(), reference.UID(), sdmTestKeys()).ConfigureSDM(plan); err != nil {
		t.Fatalf("reference run: %v", err)
	}

	afterSettings := func() func(int, []byte) bool {
		seen := false
		return func(_ int, cmd []byte) bool {
			if seen {
				return true
			}
			seen = isChangeSettings(cmd)
			return false
		}
	}
	secondUpdate := func() func(int, []byte) bool {
		updates := 0
		return func(_ int, cmd []byte) bool {
			if isUpdateBinary(cmd) {
				updates++
				return updates == 2
			}
			return false
		}
	}

	cases := []struct {
		name      string
		failAt    func(n int, cmd []byte) bool
		dropReply bool
		want      nfc.SDMState
		sdmOn     bool
	}{
		{name: "before any write", failAt: func(int, []byte) bool { return true }, want: nfc.SDMNothingWritten},
		{name: "NDEF write interrupted", failAt: secondUpdate(), want: nfc.SDMNDEFIndeterminate},
		{name: "settings change lost on the way", failAt: func(_ int, cmd []byte) bool { return isChangeSettings(cmd) }, want: nfc.SDMSettingsIndeterminate},
		{name: "settings change applied, reply lost", failAt: func(_ int, cmd []byte) bool { return isChangeSettings(cmd) }, dropReply: true, want: nfc.SDMSettingsIndeterminate, sdmOn: true},
		{name: "read-back fails", failAt: afterSettings(), want: nfc.SDMConfiguredUnverified, sdmOn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := sdmTestCard()
			faulty := &faultyTransport{CardTransport: card.Transport(), failAt: tc.failAt, dropReply: tc.dropReply}
			_, err := nfc.NewNTAG424Session(faulty, card.UID(), sdmTestKeys()).ConfigureSDM(plan)
			if err == nil {
				t.Fatal("ConfigureSDM succeeded over a failing link")
			}
			got, ok := nfc.SDMStateOf(err)
			if !ok || got != tc.want {
				t.Fatalf("state = %q (reported: %v), want %q; err: %v", got, ok, tc.want, err)
			}
			if !strings.Contains(err.Error(), string(tc.want)) {
				t.Errorf("message %q does not name the state", err)
			}
			if on := card.NTAG424FileSettings(ntag424.NDEFFileNo).SDMEnabled; on != tc.sdmOn {
				t.Errorf("SDM enabled on the card = %v, want %v", on, tc.sdmOn)
			}

			res, err := nfc.NewNTAG424Session(card.Transport(), card.UID(), sdmTestKeys()).ConfigureSDM(plan)
			if err != nil || res.Tap == nil {
				t.Fatalf("run again: %v, %+v, want a verified configuration", err, res)
			}
			if !bytes.Equal(card.NTAG424FileData(ntag424.NDEFFileNo), reference.NTAG424FileData(ntag424.NDEFFileNo)) ||
				card.NTAG424FileSettings(ntag424.NDEFFileNo) != reference.NTAG424FileSettings(ntag424.NDEFFileNo) {
				t.Error("the card after the repair differs from one configured in a single run")
			}
		})
	}
}
