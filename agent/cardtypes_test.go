package agent

import (
	"sync"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The console and the tray change the filter while the goroutine draining the
// reader is reading it. It used to be a bare map shared between them, which
// races, and a map under concurrent read and write can abort the process
// outright rather than merely returning the wrong answer.
func TestCardTypeFilterUnderConcurrentUse(t *testing.T) {
	a := runningAgent(t, 9491)
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			a.SetAllowCardType("NTAG215", i%2 == 0)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_ = a.IsCardTypeAllowed("NTAG215")
			_ = a.AllowedCardTypesLength()
		}
	}()

	wg.Wait()
}

// An empty filter admits everything, which is what an agent with no card-type
// preference must do. Naming one narrows it to that one.
func TestCardTypeFilterSemantics(t *testing.T) {
	f := newCardTypeFilter()

	if !f.isAllowed("NTAG215") {
		t.Error("an empty filter must admit every type")
	}
	if f.explicitlyAllowed("NTAG215") {
		t.Error("an empty filter names no type")
	}

	f.allow("NTAG215")
	if !f.isAllowed("NTAG215") || !f.explicitlyAllowed("NTAG215") {
		t.Error("a named type must be admitted")
	}
	if f.isAllowed("MIFARE Classic 1K") {
		t.Error("naming one type must exclude the others")
	}

	f.disallow("NTAG215")
	if !f.isAllowed("MIFARE Classic 1K") {
		t.Error("emptying the filter must admit every type again")
	}

	// allowAll names every type rather than emptying, which is what the tray
	// reads back to tick its menu.
	f.allowAll(nfc.GetAllCardTypes())
	if f.len() != len(nfc.GetAllCardTypes()) {
		t.Errorf("len = %d, want every known type named", f.len())
	}
}
