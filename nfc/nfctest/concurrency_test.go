package nfctest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestConcurrency_InterleavedOperations hammers a reader with concurrent writes
// and capability reads. Both go through the reader's operation mutex, so this
// stresses the serialization, the per-operation goroutines, and the shared
// state (cache, candidate keys, emulator) under the race detector. The final
// write confirms the reader is still functional afterwards.
func TestConcurrency_InterleavedOperations(t *testing.T) {
	card := NTAG215("04A1B2C3D4E5F6").WithText("seed")
	reader := NewEmulatedReader(t, card)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				_, _ = reader.WriteMessageWithResult(
					textMessage(fmt.Sprintf("w%d-%d", n, j)),
					nfc.WriteOptions{Overwrite: true, Index: -1})
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				_, _ = reader.GetCapabilities()
			}
		}()
	}
	wg.Wait()

	res, err := reader.WriteMessageWithResult(textMessage("final"), nfc.WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("final write after concurrency: %v", err)
	}
	if !res.Verified {
		t.Error("expected final write to verify after concurrent load")
	}
}

// TestConcurrency_TapChurn races card present/remove churn against concurrent
// writers. Individual writes may fail (no card present at that instant) — the
// point is that the reader and device handle the churn without data races,
// panics, or deadlocks (validated under -race).
func TestConcurrency_TapChurn(t *testing.T) {
	reader := NewEmulatedReader(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 150; i++ {
			reader.Present(NTAG215("04A1B2C3D4E5F6").WithText("x"))
			reader.Remove("04A1B2C3D4E5F6")
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_, _ = reader.WriteMessageWithResult(textMessage("c"), nfc.WriteOptions{Overwrite: true, Index: -1})
			}
		}()
	}
	wg.Wait()
}
