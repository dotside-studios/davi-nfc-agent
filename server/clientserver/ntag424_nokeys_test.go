package clientserver

import (
	"context"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// An operation that needs a key, with none held, tells the operator how keys
// are loaded and carries no key material.
func TestNTAG424OpWithoutKeysSaysHowToLoadThem(t *testing.T) {
	card := nfctest.NTAG424("04A1B2C3D4E5F6", nfctest.NTAG424WithKeys(map[byte][]byte{0: surfaceKey0}))
	lanes := nfctest.NewEmulatedLanes(t, surfaceLane)
	lanes.SetMode(nfc.ModeReadOnly)
	lanes.Present(surfaceLane, card)
	awaitCardOnReader(t, lanes.Supervisor, surfaceLane)
	s := newTagOps(Config{Tags: lanes.Supervisor})

	_, err := s.NTAG424(context.Background(), ntag424Req(protocol.NTAG424RequestPayload{Op: "readSig"}))
	if err == nil {
		t.Fatal("readSig worked with no keys held")
	}
	if got := codeOf(err); got != protocol.ErrCodeAuthFailed {
		t.Errorf("code = %q, want %q", got, protocol.ErrCodeAuthFailed)
	}
	if !strings.Contains(err.Error(), "-keys <file>") {
		t.Errorf("error %q does not say keys are loaded with -keys", err)
	}
}
