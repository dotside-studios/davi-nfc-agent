package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestCapabilityMatchesBehavior cross-checks each family's advertised
// capabilities against what the driver actually does on the emulator. This is
// the automated guard against capabilities that lie about behavior (e.g. the
// MIFARE Classic "CanLock=true but MakeReadOnly fails" bug): CanWrite must match
// whether a write succeeds, and CanLock must match whether locking succeeds.
func TestCapabilityMatchesBehavior(t *testing.T) {
	for _, fam := range allFamilies() {
		t.Run(fam.name, func(t *testing.T) {
			caps := nfc.GetTagCapabilities(fam.make())

			// CanWrite vs actual write (fresh tag, since WriteData mutates).
			werr := fam.make().WriteData(sampleNDEF)
			if (werr == nil) != caps.CanWrite {
				t.Errorf("CanWrite=%v but WriteData returned err=%v", caps.CanWrite, werr)
			}

			// CanLock vs actual MakeReadOnly (fresh tag, since it mutates).
			locker, ok := fam.make().(nfc.TagLocker)
			if !ok {
				t.Fatal("tag should implement TagLocker")
			}
			lerr := locker.MakeReadOnly()
			if (lerr == nil) != caps.CanLock {
				t.Errorf("CanLock=%v but MakeReadOnly returned err=%v", caps.CanLock, lerr)
			}
		})
	}
}
