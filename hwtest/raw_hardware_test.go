//go:build hardware

package hwtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/scenario"
)

// TestRawFraming is the check of issue #96: raw (framing-level) frames to an
// NTAG21x or Ultralight EV1 through the path the server uses, on whichever
// reader DAVI_HW_READER selects. It passes on a reader that carries raw
// exchanges when the replies have the lengths the issue lists, and on one that
// does not when every frame is refused as not supported with nothing sent.
//
// Run it once per reader and tag, and send back the fixture it writes.
func TestRawFraming(t *testing.T) {
	hw := openHardware(t)

	kind := strings.ToLower(hw.tag.Card.Type)
	if !strings.Contains(kind, "ntag2") && !strings.Contains(kind, "ultralight") {
		t.Skipf("the frames of issue #96 are for an NTAG21x or an Ultralight EV1, the tag on the reader is %s", hw.tag.Card.Type)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := scenario.RunRawFraming(ctx, t, scenario.RawConfig{Sup: hw.sup, Rec: hw.rec, Tag: hw.tag})

	save(t, "raw-"+fileSafe(out.Reader)+"-"+fileSafe(hw.tag.Card.Type)+"-"+strings.ToLower(hw.tag.UID), out)
	t.Logf("reader method %q, canTransceiveRaw %v", out.Method, out.CanTransceiveRaw)
}
