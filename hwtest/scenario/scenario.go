// Package scenario holds the hardware test procedures for issues #96 and #99 as
// ordinary functions over an nfc.Supervisor.
//
// The procedures live here, rather than in the hardware-tagged tests, so the
// same code runs in two places: against a real reader from package hwtest, and
// against emulators in the normal test suite, which is how it is known to work
// before anyone carries it to a card. Both go through the Supervisor, the path
// the server takes for a client request.
package scenario

import (
	"context"
	"fmt"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TB is the part of testing.TB the procedures use, so they can be driven by a
// test or by a tool.
type TB interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

// Watcher follows what a supervisor's readers scan, so a procedure can have the
// card the agent read. Create it before the supervisor starts: a scan emitted
// before there was a listener is not replayed.
type Watcher struct {
	scans <-chan nfc.NFCData
	stop  func()
}

// Watch subscribes to sup's scans.
func Watch(sup *nfc.Supervisor) *Watcher {
	ch, stop := sup.Scans().Channel(64)
	return &Watcher{scans: ch, stop: stop}
}

// Close ends the subscription.
func (w *Watcher) Close() {
	if w.stop != nil {
		w.stop()
	}
}

// Tag is a tag on a reader, as the agent scanned it.
type Tag struct {
	Device string
	UID    string
	Card   *nfc.Card
}

// Wait blocks until a reader scans a card. device selects a reader by exact
// name, or any when empty.
func (w *Watcher) Wait(ctx context.Context, device string) (Tag, error) {
	for {
		select {
		case <-ctx.Done():
			return Tag{}, fmt.Errorf("no tag was scanned: %w", ctx.Err())
		case data, ok := <-w.scans:
			if !ok {
				return Tag{}, fmt.Errorf("the scan stream closed")
			}
			if data.Card == nil || (device != "" && data.Device != device) {
				continue
			}
			return Tag{Device: data.Device, UID: data.Card.UID, Card: data.Card}, nil
		}
	}
}

// WaitTimeout is Wait with a deadline.
func (w *Watcher) WaitTimeout(device string, d time.Duration) (Tag, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return w.Wait(ctx, device)
}
