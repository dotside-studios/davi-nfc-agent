package pcsc

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/hwtest/scenario"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
)

// The hardware harness for issue #96 (package hwtest) run against an emulated
// reader: the real manager, supervisor and device code, over a PC/SC context
// that holds an emulated NTAG. It proves the harness records and asserts what it
// should before it is carried to a reader, and writes the fixtures the replay
// below runs. The bytes come from this package's reading of the manuals, so they
// are not evidence about any reader: only a capture from hardware is.

var updateFixtures = flag.Bool("update-hwfixtures", false, "rewrite hwtest/fixture/testdata raw fixtures from the emulators")

const fixtureDir = "../../hwtest/fixture/testdata"

// atrCard is a scardCard with an ATR naming an Ultralight family card.
type atrCard struct {
	*scriptCard
}

func (c atrCard) Status() (*cardStatus, error) {
	return &cardStatus{Atr: []byte{0x3B, 0x8F, 0x80, 0x01, 0x80, 0x4F, 0x0C, 0xA0, 0x00, 0x00, 0x03, 0x06, 0x03, 0x00, 0x03, 0x00, 0x00, 0x00, 0x00, 0x68}}, nil
}

// cardContext lists one reader holding one card, as PC/SC reports it.
type cardContext struct {
	reader string
	card   scardCard
}

func (c *cardContext) ListReaders() ([]string, error) { return []string{c.reader}, nil }

func (c *cardContext) GetStatusChange(states []readerState, timeout time.Duration) error {
	if timeout == 0 {
		for i := range states {
			states[i].EventState = statePresent
		}
		return nil
	}
	time.Sleep(min(timeout, 20*time.Millisecond))
	return errTimeout
}

func (c *cardContext) Connect(string, shareMode, protocol) (scardCard, error) { return c.card, nil }
func (c *cardContext) Cancel() error                                          { return nil }
func (c *cardContext) Release() error                                         { return nil }

// harnessSetup starts a supervisor on a manager whose reader holds card, with
// the observer installed.
func harnessSetup(t *testing.T, reader string, card scardCard) (*nfc.Supervisor, *fixture.Recorder, scenario.Tag) {
	t.Helper()
	rec := &fixture.Recorder{}
	SetObserver(Observer{Connected: rec.Connected, Transmit: rec.Transmit})
	t.Cleanup(func() { SetObserver(Observer{}) })

	m := &Manager{ctx: &cardContext{reader: reader, card: card}, stopped: make(chan struct{})}
	sup, err := nfc.NewSupervisor(m, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	watch := scenario.Watch(sup)
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sup.Stop(); watch.Close(); m.Close() })

	tag, err := watch.WaitTimeout("", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return sup, rec, tag
}

type rawCase struct {
	name   string
	reader string
	method string
	card   func() *atrCard
}

// withUID makes a card answer GET UID, which a reader answers itself and an
// emulated tag does not.
func withUID(uid string, card *scriptCard) *atrCard {
	inner := card.fn
	card.fn = func(cmd []byte) ([]byte, error) {
		if bytes.Equal(cmd, nfc.GetUIDAPDU()) {
			raw, _ := nfc.HexToBytes(uid)
			return append(raw, 0x90, 0x00), nil
		}
		return inner(cmd)
	}
	return &atrCard{card}
}

func rawCases() []rawCase {
	const uid = "04A1B2C3D4E5F6"
	return []rawCase{
		{"acr122", "ACS ACR122U PICC Interface 00 00 (emulated)", "acr122", func() *atrCard {
			return withUID(uid, acr122Card(nfctest.NTAG215(uid).Transport()))
		}},
		{"part3", "ACS ACR1252 PICC Reader (emulated)", "part3", func() *atrCard {
			emu := nfctest.NTAG215(uid).Transport()
			part3 := transparentCard(emu.(nfc.RawCardTransport))
			return withUID(uid, &scriptCard{fn: func(cmd []byte) ([]byte, error) {
				if len(cmd) > 1 && cmd[0] == 0xFF && cmd[1] == 0xC2 {
					return part3.fn(cmd)
				}
				return emu.Transceive(cmd)
			}})
		}},
		{"none", "Some Other Reader (emulated)", "none", func() *atrCard {
			emu := nfctest.NTAG215(uid).Transport()
			return withUID(uid, &scriptCard{fn: func(cmd []byte) ([]byte, error) {
				if len(cmd) > 1 && cmd[0] == 0xFF && cmd[1] == 0xC2 {
					return []byte{0x6D, 0x00}, nil
				}
				return emu.Transceive(cmd)
			}})
		}},
	}
}

func TestHarnessRawFramingAgainstEmulatedReaders(t *testing.T) {
	for _, tc := range rawCases() {
		t.Run(tc.name, func(t *testing.T) {
			sup, rec, tag := harnessSetup(t, tc.reader, tc.card())

			out := scenario.RunRawFraming(context.Background(), t, scenario.RawConfig{Sup: sup, Rec: rec, Tag: tag})
			if out.Method != tc.method {
				t.Fatalf("method = %q, want %q", out.Method, tc.method)
			}
			if out.NDEFAfter == nil || !out.NDEFAfter.Read {
				t.Errorf("NDEF read after the frames: %+v", out.NDEFAfter)
			}
			if len(out.Exchanges) != 5 {
				t.Fatalf("exchanges = %d, want 5", len(out.Exchanges))
			}
			wantWire := map[string]int{"acr122": 1, "part3": 3, "none": 0}[tc.method]
			for _, ex := range out.Exchanges {
				if len(ex.Wire) != wantWire {
					t.Errorf("%s: %d wire exchanges, want %d", ex.Name, len(ex.Wire), wantWire)
				}
			}
			if err := replayRaw(out); err != nil {
				t.Errorf("the fixture the procedure wrote does not replay: %v", err)
			}
			if *updateFixtures {
				path, err := fixture.Write(fixtureDir, "raw-emulator-"+tc.name, out)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", path)
			}
		})
	}
}

// replayRaw feeds what the reader recorded back through the unwrap functions
// and checks they decode to the recorded frames and replies.
func replayRaw(f *fixture.Raw) error {
	for _, ex := range f.Exchanges {
		frame, err := hexBytes(ex.Frame)
		if err != nil {
			return err
		}
		reply, err := hexBytes(ex.Reply)
		if err != nil {
			return err
		}

		switch f.Method {
		case "none":
			if len(ex.Wire) != 0 {
				return errors.New(ex.Name + ": a reader with no raw support was sent commands")
			}
		case "acr122":
			if len(ex.Wire) != 1 {
				return errors.New(ex.Name + ": want one Direct Transmit")
			}
			if err := replayThru(ex, frame, reply); err != nil {
				return err
			}
		case "part3":
			if len(ex.Wire) != 3 {
				return errors.New(ex.Name + ": want session start, transceive and session end")
			}
			if err := replayTransparent(ex, frame, reply); err != nil {
				return err
			}
		default:
			return errors.New("unknown method " + f.Method)
		}
	}
	return nil
}

func replayThru(ex fixture.RawExchange, frame, reply []byte) error {
	w := ex.Wire[0]
	cmd, err := w.CommandBytes()
	if err != nil {
		return err
	}
	want, err := acr122Thru(frame)
	if err != nil {
		return err
	}
	if !bytes.Equal(cmd, want) {
		return errors.New(ex.Name + ": the recorded Direct Transmit is not what acr122Thru builds for the frame")
	}
	if w.Error != "" {
		return errors.New(ex.Name + ": transmit failed: " + w.Error)
	}
	resp, err := w.ResponseBytes()
	if err != nil {
		return err
	}
	got, err := acr122Unthru(resp)
	return checkDecoded(ex, got, err, reply)
}

func replayTransparent(ex fixture.RawExchange, frame, reply []byte) error {
	cmds := make([][]byte, len(ex.Wire))
	for i, w := range ex.Wire {
		c, err := w.CommandBytes()
		if err != nil {
			return err
		}
		cmds[i] = c
	}
	exchange, err := transparentTransceive(frame)
	if err != nil {
		return err
	}
	if !bytes.Equal(cmds[0], transparentStartSession()) || !bytes.Equal(cmds[1], exchange) || !bytes.Equal(cmds[2], transparentEndSession()) {
		return errors.New(ex.Name + ": the recorded session is not start, the transceive for the frame and end")
	}
	if ex.Wire[1].Error != "" {
		return errors.New(ex.Name + ": transmit failed: " + ex.Wire[1].Error)
	}
	resp, err := ex.Wire[1].ResponseBytes()
	if err != nil {
		return err
	}
	got, err := transparentReply(resp)
	return checkDecoded(ex, got, err, reply)
}

// checkDecoded compares what a parser made of the reader's answer with what the
// agent returned at the time: the reply, or an error.
func checkDecoded(ex fixture.RawExchange, got []byte, err error, reply []byte) error {
	switch {
	case ex.Error != "" && err == nil:
		return errors.New(ex.Name + ": the agent failed with " + ex.Error + " but the parser decodes the answer")
	case ex.Error == "" && err != nil:
		return errors.New(ex.Name + ": the agent returned a reply but the parser fails: " + err.Error())
	case ex.Error == "" && !bytes.Equal(got, reply):
		return errors.New(ex.Name + ": the parser decodes the answer differently from the recorded reply")
	}
	return nil
}

func hexBytes(s string) ([]byte, error) { return fixture.Wire{Command: s}.CommandBytes() }

// Every raw fixture under hwtest/fixture/testdata, emulated or captured on a
// reader, must decode through the parsers. A captured one is evidence about the
// framing; an emulated one is only a check of the replay.
func TestReplayRawFixtures(t *testing.T) {
	paths, err := fixture.Find(fixtureDir, fixture.KindRawFraming)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no raw framing fixtures under hwtest/fixture/testdata")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := fixture.LoadRaw(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := replayRaw(f); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestReplayRawCatchesDamage(t *testing.T) {
	paths, err := fixture.Find(fixtureDir, fixture.KindRawFraming)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"acr122", "part3"} {
		var f *fixture.Raw
		for _, p := range paths {
			if loaded, err := fixture.LoadRaw(p); err == nil && loaded.Method == method {
				f = loaded
			}
		}
		if f == nil {
			t.Logf("no %s fixture to damage", method)
			continue
		}
		t.Run(method, func(t *testing.T) {
			if err := replayRaw(f); err != nil {
				t.Fatal(err)
			}
			ex := &f.Exchanges[1]
			ex.Wire[len(ex.Wire)/2].Command = "FF0000000100"
			if err := replayRaw(f); err == nil {
				t.Error("a recorded command that does not match its frame was accepted")
			}
		})
	}
}
