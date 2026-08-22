package traymenu

import "sync"

// Discard returns a container that takes items and does nothing with them.
//
// It is for a build with nothing to draw them: a feature that always has a menu
// to fill then needs no branch for the case where nobody is drawing one, and
// its items behave — they can be relabelled, checked and hidden, they are just
// not on any tray. Nothing can click them, so their handlers never run.
//
// The container is shared and lives for the life of the process.
func Discard() Container {
	discardOnce.Do(func() { discard = New(nullDriver{}) })
	return discard
}

var (
	discardOnce sync.Once
	discard     *Menu
)

// nullDriver is a Driver with no platform behind it.
type nullDriver struct{}

func (nullDriver) Run(onReady, onExit func()) {
	// Ready at once and never running: there is nothing to show and nothing to
	// wait for.
	if onReady != nil {
		onReady()
	}
	if onExit != nil {
		onExit()
	}
}

func (nullDriver) Quit()                       {}
func (nullDriver) SetIcon([]byte)              {}
func (nullDriver) SetTooltip(string)           {}
func (nullDriver) AddItem(Native, Spec) Native { return nullItem{} }
func (nullDriver) AddSeparator(Native)         {}
func (nullDriver) RemoveItem(Native)           {}

// nullItem is one item nobody can see.
type nullItem struct{}

func (nullItem) SetTitle(string)   {}
func (nullItem) SetTooltip(string) {}
func (nullItem) SetChecked(bool)   {}
func (nullItem) SetEnabled(bool)   {}
func (nullItem) SetVisible(bool)   {}

// Clicks reports that the item can never be clicked, which is what keeps a
// watcher goroutine off every discarded item.
func (nullItem) Clicks() <-chan struct{} { return nil }
