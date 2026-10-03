package nfctest

import (
	"bytes"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The chaining applet is a small ISO-DEP card for the status words that ask a
// reader to do more: 61xx (more data, fetch it with GET RESPONSE) and 6Cxx
// (wrong Le, resend with the right one). It models only that, as a T=0 style
// card does, and nothing else about a real applet.
//
// One data command, GET DATA (00 CA P1 P2 [Le]), selected by P1:
//
//	ChainP1Chunked  holds ChainChunkedLen bytes and answers 61 10 with no data.
//	                Each GET RESPONSE returns up to its Le from what is held,
//	                with 61 xx while more remains and 90 00 with the last of it,
//	                so a 16 byte Le takes the card through 61 10 twice and then
//	                90 00.
//	ChainP1ShortLe  holds ChainShortLeLen bytes and answers 6C 08 unless Le is
//	                exactly 8, then returns them with 90 00.
const (
	ChainP1Chunked  = 0x00
	ChainP1ShortLe  = 0x01
	ChainChunkedLen = 32
	ChainShortLeLen = 8
	chainChunk      = 0x10
)

// ChainChunked is the data ChainP1Chunked returns, in order.
func ChainChunked() []byte {
	out := make([]byte, ChainChunkedLen)
	for i := range out {
		out[i] = byte(0xA0 + i)
	}
	return out
}

// ChainShortLe is the data ChainP1ShortLe returns.
func ChainShortLe() []byte {
	return []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
}

// ChainingApplet is an emulated card running the chaining applet. Its tag is
// driven as a generic ISO 14443-4 card.
type ChainingApplet struct {
	*EmulatedCard
	applet *chainApplet
}

// NewChainingApplet constructs a card running the chaining applet.
func NewChainingApplet(uid string) *ChainingApplet {
	a := &chainApplet{}
	return &ChainingApplet{
		EmulatedCard: newCard(nfc.DetectedISO14443_4, uid, a),
		applet:       a,
	}
}

// Commands returns every command the card has received, in order.
func (c *ChainingApplet) Commands() [][]byte {
	c.applet.mu.Lock()
	defer c.applet.mu.Unlock()
	out := make([][]byte, len(c.applet.received))
	for i, cmd := range c.applet.received {
		out[i] = append([]byte(nil), cmd...)
	}
	return out
}

type chainApplet struct {
	mu       sync.Mutex
	received [][]byte
	pending  []byte
}

func (a *chainApplet) IsCardPresent() bool { return true }

func (a *chainApplet) Transceive(cmd []byte) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.received = append(a.received, append([]byte(nil), cmd...))

	if len(cmd) < 4 {
		return apduSW(0x6700), nil
	}
	switch cmd[1] {
	case 0xCA:
		return a.getData(cmd), nil
	case nfc.INSGetResponse:
		return a.getResponse(cmd), nil
	}
	return apduSW(0x6D00), nil
}

func (a *chainApplet) getData(cmd []byte) []byte {
	switch cmd[2] {
	case ChainP1Chunked:
		a.pending = ChainChunked()
		return apduSW(0x6100 | chainChunk)
	case ChainP1ShortLe:
		if len(cmd) != 5 || cmd[4] != ChainShortLeLen {
			return apduSW(0x6C00 | ChainShortLeLen)
		}
		return apduData(ChainShortLe())
	}
	return apduSW(0x6A86)
}

func (a *chainApplet) getResponse(cmd []byte) []byte {
	if len(a.pending) == 0 {
		return apduSW(0x6985)
	}
	le := int(cmd[len(cmd)-1])
	if len(cmd) != 5 || le == 0 {
		le = len(a.pending)
	}
	return a.release(le)
}

func (a *chainApplet) release(n int) []byte {
	n = min(n, len(a.pending))
	out := bytes.Clone(a.pending[:n])
	a.pending = a.pending[n:]
	if len(a.pending) == 0 {
		return apduData(out)
	}
	return append(out, 0x61, byte(min(len(a.pending), 0xFF)))
}
