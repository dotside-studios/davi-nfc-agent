package traymenu

// Discard returns a Driver that draws nothing. Items on a menu running on it
// can still be relabelled, checked, hidden and removed; they are never shown
// and never clicked.
//
// It is what a menu runs on where there is no tray, so a plugin adding a menu
// entry need not ask whether anything is drawing one. Run calls onReady and
// onExit and returns rather than blocking.
func Discard() Driver { return discard{} }

type discard struct{}

func (discard) Run(onReady, onExit func()) {
	if onReady != nil {
		onReady()
	}
	if onExit != nil {
		onExit()
	}
}

func (discard) Quit()               {}
func (discard) SetIcon([]byte)      {}
func (discard) SetTooltip(string)   {}
func (discard) RemoveItem(Native)   {}
func (discard) AddSeparator(Native) {}

func (discard) AddItem(_ Native, spec Spec) Native { return discardItem{} }

// discardItem accepts every change and reports no clicks. A nil click channel
// is what tells Menu there is nothing to watch, so no goroutine is started per
// item either.
type discardItem struct{}

func (discardItem) SetTitle(string)         {}
func (discardItem) SetTooltip(string)       {}
func (discardItem) SetChecked(bool)         {}
func (discardItem) SetEnabled(bool)         {}
func (discardItem) SetVisible(bool)         {}
func (discardItem) Clicks() <-chan struct{} { return nil }
