package tray

import (
	"testing"
	"time"

	"fyne.io/systray"
)

// deliverClick reproduces how systray hands a click to a menu item: a
// non-blocking send on an unbuffered channel, dropped when nothing is
// receiving. See systray.go, "in case no one waiting for the channel".
func deliverClick(item *systray.MenuItem) bool {
	select {
	case item.ClickedCh <- struct{}{}:
		return true
	default:
		return false
	}
}

// TestAClickWithNoReceiverIsLost is the reason onClick exists. The dynamic rows
// used to be read by a non-blocking poll that ran only after some other menu
// item fired, so for almost all of the time nothing was waiting on them and
// every click on a reader, a card type filter, an origin or a paired device was
// dropped before the tray saw it.
func TestAClickWithNoReceiverIsLost(t *testing.T) {
	item := &systray.MenuItem{ClickedCh: make(chan struct{})}

	if deliverClick(item) {
		t.Error("a click was delivered with nothing waiting on the channel")
	}
}

// TestOnClickReceivesEveryClick pins the fix: a receiver that stays parked for
// the item's life takes clicks whenever they come, not only in the window after
// another menu event.
func TestOnClickReceivesEveryClick(t *testing.T) {
	item := &systray.MenuItem{ClickedCh: make(chan struct{})}

	handled := make(chan struct{}, 8)
	onClick(item, func() { handled <- struct{}{} })

	for i := 1; i <= 5; i++ {
		if !clickEventually(item) {
			t.Fatalf("click %d was still being dropped a second in", i)
		}
		select {
		case <-handled:
		case <-time.After(2 * time.Second):
			t.Fatalf("click %d never reached the handler", i)
		}
	}
}

// clickEventually retries the delivery systray would attempt, so the test does
// not depend on the receiver having been scheduled yet.
func clickEventually(item *systray.MenuItem) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if deliverClick(item) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
