// Package fynetray draws a [traymenu.Menu] on the real system tray, through
// fyne.io/systray.
//
// It is a package of its own so that traymenu is a menu model and nothing else.
// fyne.io/systray talks to Cocoa, so anything importing it needs cgo on macOS,
// and the agent hands plugins a menu without needing one.
//
//	menu := traymenu.New(fynetray.New())
//	menu.Run(onReady, onExit)
package fynetray

import (
	"fyne.io/systray"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// New returns the driver backed by fyne.io/systray, the real tray. There is
// one tray icon per process, so a second menu on this driver would add its
// items to the first one's.
func New() traymenu.Driver { return driver{} }

type driver struct{}

func (driver) Run(onReady, onExit func()) { systray.Run(onReady, onExit) }
func (driver) Quit()                      { systray.Quit() }
func (driver) SetIcon(icon []byte)        { systray.SetIcon(icon) }
func (driver) SetTooltip(tooltip string)  { systray.SetTooltip(tooltip) }

func (driver) AddItem(parent traymenu.Native, spec traymenu.Spec) traymenu.Native {
	if parent == nil {
		if spec.Checkbox {
			return &item{item: systray.AddMenuItemCheckbox(spec.Title, spec.Tooltip, spec.Checked)}
		}
		return &item{item: systray.AddMenuItem(spec.Title, spec.Tooltip)}
	}

	p := parent.(*item)
	if spec.Checkbox {
		return &item{item: p.item.AddSubMenuItemCheckbox(spec.Title, spec.Tooltip, spec.Checked)}
	}
	return &item{item: p.item.AddSubMenuItem(spec.Title, spec.Tooltip)}
}

func (driver) RemoveItem(native traymenu.Native) {
	native.(*item).item.Remove()
}

func (driver) AddSeparator(parent traymenu.Native) {
	if parent == nil {
		systray.AddSeparator()
		return
	}
	parent.(*item).item.AddSeparator()
}

// item adapts a systray menu item to traymenu.Native, folding the library's
// pairs of methods back into settable state.
type item struct {
	item *systray.MenuItem
}

func (f *item) SetTitle(title string)     { f.item.SetTitle(title) }
func (f *item) SetTooltip(tooltip string) { f.item.SetTooltip(tooltip) }

func (f *item) SetChecked(checked bool) {
	if checked {
		f.item.Check()
		return
	}
	f.item.Uncheck()
}

func (f *item) SetEnabled(enabled bool) {
	if enabled {
		f.item.Enable()
		return
	}
	f.item.Disable()
}

func (f *item) SetVisible(visible bool) {
	if visible {
		f.item.Show()
		return
	}
	f.item.Hide()
}

func (f *item) Clicks() <-chan struct{} { return f.item.ClickedCh }
