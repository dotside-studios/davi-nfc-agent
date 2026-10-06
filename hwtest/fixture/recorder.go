package fixture

import (
	"strings"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// ReaderInfo is what the reader reported about itself when it was opened.
type ReaderInfo struct {
	Name             string
	ATR              []byte
	Method           string
	CanTransceiveRaw bool
}

// Recorder collects the exchanges a reader makes. Its Connected and Transmit
// methods match the hooks of pcsc.Observer, so it is installed with
//
//	pcsc.SetObserver(pcsc.Observer{Connected: rec.Connected, Transmit: rec.Transmit})
//
// and sees exactly what the real driver sent. It is safe for concurrent use.
type Recorder struct {
	mu    sync.Mutex
	only  string
	wires []Wire
	info  ReaderInfo
	have  bool
}

// Only restricts the recorder to the reader whose name contains substr, and
// forgets anything recorded from another. Without it every reader is recorded.
func (r *Recorder) Only(substr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.only = substr
}

func (r *Recorder) wants(reader string) bool {
	return r.only == "" || strings.Contains(strings.ToLower(reader), strings.ToLower(r.only))
}

// Connected records what a reader said about itself when it was opened.
func (r *Recorder) Connected(reader string, atr []byte, method string, canTransceiveRaw bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.wants(reader) {
		return
	}
	r.info = ReaderInfo{Name: reader, ATR: append([]byte(nil), atr...), Method: method, CanTransceiveRaw: canTransceiveRaw}
	r.have = true
}

// Transmit records one exchange with the card.
func (r *Recorder) Transmit(reader string, command, response []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.wants(reader) {
		return
	}
	w := Wire{Command: Hex(command)}
	if err != nil {
		w.Error = err.Error()
	} else {
		w.Response = Hex(response)
	}
	r.wires = append(r.wires, w)
}

// Mark returns a position in the recording. Since gives what was recorded after
// it.
func (r *Recorder) Mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.wires)
}

// Since returns the exchanges recorded after mark.
func (r *Recorder) Since(mark int) []Wire {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mark < 0 || mark > len(r.wires) {
		mark = len(r.wires)
	}
	return append([]Wire(nil), r.wires[mark:]...)
}

// Info reports what the reader said about itself, and false until it has.
func (r *Recorder) Info() (ReaderInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info, r.have
}

// Tap returns a card transport that records every exchange with inner, for a
// run against an emulator, which has no PC/SC reader to observe. reader names
// the recording.
func (r *Recorder) Tap(inner nfc.CardTransport, reader string) nfc.CardTransport {
	return &tapTransport{inner: inner, rec: r, reader: reader}
}

type tapTransport struct {
	inner  nfc.CardTransport
	rec    *Recorder
	reader string
}

func (t *tapTransport) Transceive(cmd []byte) ([]byte, error) {
	resp, err := t.inner.Transceive(cmd)
	t.rec.Transmit(t.reader, cmd, resp, err)
	return resp, err
}

func (t *tapTransport) IsCardPresent() bool { return t.inner.IsCardPresent() }
