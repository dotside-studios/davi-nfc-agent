package nfctest

import "fmt"

// Fault injection on an EmulatedCard.
//
// A real tap is not the clean, cooperative exchange the happy-path tests model.
// The field is noisy, a card is lifted the instant a write is acknowledged, and
// silicon occasionally reports a success it never persisted. These builders let
// a test declare a card that misbehaves the way real cards do, chaining onto the
// same card builder that preloads content:
//
//	card := nfctest.NTAG215("04A1B2C3D4E5F6").WithText("seed").FailingWrites(1)
//	reader := nfctest.NewEmulatedReader(t, card)
//
// Set faults BEFORE presenting the card to a reader: the reader's background
// poll starts exchanging with the card immediately.
//
// FailingWrites and Corrupting model the write path and apply only to the
// NTAG/Ultralight family, the page-oriented tags the agent writes NDEF to.
// RemovingAfter models the card leaving the field and works on every family.

// setFailWrites arms the next n write commands to NAK, under the emulator lock
// so it is safe to set while the reader's poll is already running.
func (e *memEmulator) setFailWrites(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failWrites = n
}

// setCorrupt makes every write store inverted bytes, under the emulator lock.
func (e *memEmulator) setCorrupt(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.corrupt = v
}

// FailingWrites makes the next n write commands NAK before the bytes take,
// modelling a flaky field or a reader momentarily losing the card mid-write. The
// write path treats a NAK as transient and retries within its attempt budget, so
// a card that fails fewer times than that budget still ends up written and
// verified; one that fails more surfaces a write error rather than a false
// success. Returns the card so it can be chained.
func (c *EmulatedCard) FailingWrites(n int) *EmulatedCard {
	mem, ok := c.transport.(*memEmulator)
	if !ok {
		panic(fmt.Sprintf("nfctest: FailingWrites is only modelled for the NTAG/Ultralight family, not %T", c.transport))
	}
	mem.setFailWrites(n)
	return c
}

// Corrupting makes every write store inverted bytes, so the verifying read-back
// can never match what was written. It models a tag that acknowledges a write it
// did not actually persist: the write path reads the tag back, sees the
// mismatch, exhausts its retries, and reports the write unverified rather than
// claiming a success that never landed. Returns the card so it can be chained.
func (c *EmulatedCard) Corrupting() *EmulatedCard {
	mem, ok := c.transport.(*memEmulator)
	if !ok {
		panic(fmt.Sprintf("nfctest: Corrupting is only modelled for the NTAG/Ultralight family, not %T", c.transport))
	}
	mem.setCorrupt(true)
	return c
}

// RemovingAfter makes the card leave the field after n transceive operations, so
// the exchange that follows returns a typed card-removed error. It models a tap
// too brief for the operation in flight — the card yanked mid-read or mid-write.
//
// The reader's background poll also transceives, so at the reader level the exact
// operation the removal lands on is not deterministic; RemovingAfter is precise
// when the card is driven directly through card.Tag(). For a deterministic
// removal at the reader level, present the card and then Remove it by UID.
// Returns the card so it can be chained.
func (c *EmulatedCard) RemovingAfter(n int) *EmulatedCard {
	rem, ok := c.transport.(interface{ setRemoveAfter(int) })
	if !ok {
		panic(fmt.Sprintf("nfctest: RemovingAfter is not modelled for %T", c.transport))
	}
	rem.setRemoveAfter(n)
	return c
}
