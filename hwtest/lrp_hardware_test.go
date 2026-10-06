//go:build hardware

package hwtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/hwtest/fixture"
	"github.com/dotside-studios/davi-nfc-agent/hwtest/scenario"
)

// TestLRP is the check of issue #99 on an NTAG 424 DNA, with the keys in
// DAVI_HW_KEYS.
//
// A tag on AES is switched to LRP only when DAVI_HW_ENABLE_LRP_UID is set and
// equals the UID of the tag on the reader. The switch is permanent. With
// anything else, the test records the AES baseline and skips the rest. A tag
// already in LRP from an earlier run skips the switch and carries on.
func TestLRP(t *testing.T) {
	keys := loadKeys(t)
	hw := openHardware(t)

	if !strings.Contains(hw.tag.Card.Type, "424") {
		t.Skipf("issue #99 is for an NTAG 424 DNA, the tag on the reader is %s", hw.tag.Card.Type)
	}

	enable := strings.TrimSpace(os.Getenv(envEnableLRPUID))
	if enable != "" && !strings.EqualFold(enable, hw.tag.UID) {
		t.Logf("%s=%s does not match the tag on the reader (%s): this tag will not be switched", envEnableLRPUID, enable, hw.tag.UID)
	}

	include := envOn(envIncludeKeys)
	if !include {
		t.Logf("%s is off, so the fixture will not hold the keys and cannot be replayed. Use a spare tag with throwaway keys and set it to 1 to make a replayable fixture", envIncludeKeys)
	}

	out := &fixture.LRP{}
	defer func() {
		save(t, fmt.Sprintf("lrp-%s-%s", strings.ToLower(hw.tag.UID), time.Now().UTC().Format("20060102-150405")), out)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	scenario.RunLRP(ctx, t, scenario.LRPConfig{
		Sup:         hw.sup,
		Rec:         hw.rec,
		Tag:         hw.tag,
		Keys:        keys.NTAG424,
		EnableUID:   enable,
		IncludeKeys: include,
		KeepSDM:     envOn(envKeepSDM),
		Warn: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "\n*** "+format+" ***\n\n", args...)
		},
	}, out)
}
