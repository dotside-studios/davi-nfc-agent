package nfctest

import (
	"fmt"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// strictTransport wraps an emulator so that a command the production driver
// builds is checked for structural well-formedness before it reaches the
// emulated silicon. A malformed APDU — one whose declared length does not match
// the bytes it carries — is a driver bug, and this turns it into a failed
// operation in any test, across every emulated tag type, rather than something
// only a real reader would reject.
//
// It reads only the framing, never the meaning: it does not reject a command it
// simply does not recognise (an emulator answers those itself), and it cannot
// catch a semantically wrong but well-formed command — a SELECT with the wrong
// P1, say, which is for each emulator's own command handling to refuse.
type strictTransport struct {
	inner nfc.CardTransport
}

func (s strictTransport) IsCardPresent() bool { return s.inner.IsCardPresent() }

func (s strictTransport) Transceive(cmd []byte) ([]byte, error) {
	if problem := malformedAPDU(cmd); problem != "" {
		return nil, fmt.Errorf("nfctest: driver built a malformed APDU: %s (% X)", problem, cmd)
	}
	return s.inner.Transceive(cmd)
}

// malformedAPDU reports a structural problem in an APDU-level command, or "" if
// it is well-formed. Emulators receive the driver's commands at APDU level, so
// the framing decode uses raw=false.
func malformedAPDU(cmd []byte) string {
	// An empty command is the driver sending nothing; leave that to the emulator.
	if len(cmd) == 0 {
		return ""
	}
	for _, w := range nfc.Explain(cmd, false).Warnings {
		if strings.Contains(w, "declared length") {
			return w
		}
	}
	return ""
}
