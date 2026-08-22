package traymenu

import "fyne.io/systray"

// Fyne returns the driver backed by fyne.io/systray, the real tray. There is
// one tray icon per process, so a second menu on this driver would add its
// items to the first one's.
func Fyne() Driver { return fyneDriver{} }

type fyneDriver struct{}

func (fyneDriver) Run(onReady, onExit func()) { systray.Run(onReady, onExit) }
func (fyneDriver) Quit()                      { systray.Quit() }
func (fyneDriver) SetIcon(icon []byte)        { systray.SetIcon(icon) }
func (fyneDriver) SetTooltip(tooltip string)  { systray.SetTooltip(tooltip) }

func (fyneDriver) AddItem(parent Native, spec Spec) Native {
	if parent == nil {
		if spec.Checkbox {
			return &fyneItem{item: systray.AddMenuItemCheckbox(spec.Title, spec.Tooltip, spec.Checked)}
		}
		return &fyneItem{item: systray.AddMenuItem(spec.Title, spec.Tooltip)}
	}

	p := parent.(*fyneItem)
	if spec.Checkbox {
		return &fyneItem{item: p.item.AddSubMenuItemCheckbox(spec.Title, spec.Tooltip, spec.Checked)}
	}
	return &fyneItem{item: p.item.AddSubMenuItem(spec.Title, spec.Tooltip)}
}

func (fyneDriver) AddSeparator(parent Native) {
	if parent == nil {
		systray.AddSeparator()
		return
	}
	parent.(*fyneItem).item.AddSeparator()
}

// fyneItem adapts a systray menu item to Native, folding the library's pairs of
// methods back into settable state.
type fyneItem struct {
	item *systray.MenuItem
}

func (f *fyneItem) SetTitle(title string)     { f.item.SetTitle(title) }
func (f *fyneItem) SetTooltip(tooltip string) { f.item.SetTooltip(tooltip) }

func (f *fyneItem) SetChecked(checked bool) {
	if checked {
		f.item.Check()
		return
	}
	f.item.Uncheck()
}

func (f *fyneItem) SetEnabled(enabled bool) {
	if enabled {
		f.item.Enable()
		return
	}
	f.item.Disable()
}

func (f *fyneItem) SetVisible(visible bool) {
	if visible {
		f.item.Show()
		return
	}
	f.item.Hide()
}

func (f *fyneItem) Clicks() <-chan struct{} { return f.item.ClickedCh }
