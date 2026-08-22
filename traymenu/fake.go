package traymenu

import (
	"strings"
	"sync"
)

// Fake is an in-memory [Driver]. It is what makes a menu testable: the whole
// tree, and every change made to it afterwards, can be read back without a
// display server.
//
//	fake := traymenu.NewFake()
//	menu := traymenu.New(fake)
//	defer menu.Close()
//
//	quit := menu.Add("Quit", traymenu.OnClick(menu.Quit))
//	quit.Click()
//
//	t.Log(fake.Render())
type Fake struct {
	mu       sync.Mutex
	icon     []byte
	tooltip  string
	children []*FakeItem

	running bool
	quit    chan struct{}
}

// NewFake returns a driver that records a menu instead of drawing one.
func NewFake() *Fake {
	return &Fake{quit: make(chan struct{})}
}

// Run calls onReady and blocks until [Fake.Quit], mirroring the real driver so
// the same startup code runs in a test. A menu can also be built without ever
// calling Run.
func (f *Fake) Run(onReady, onExit func()) {
	f.mu.Lock()
	f.running = true
	f.mu.Unlock()

	if onReady != nil {
		onReady()
	}
	<-f.quit
	if onExit != nil {
		onExit()
	}
}

// Quit returns from [Fake.Run]. Calling it twice is harmless.
func (f *Fake) Quit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		return
	}
	f.running = false
	close(f.quit)
}

// Running reports whether Run is in progress.
func (f *Fake) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// SetIcon records the tray icon.
func (f *Fake) SetIcon(icon []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.icon = icon
}

// Icon returns the icon last set.
func (f *Fake) Icon() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.icon
}

// SetTooltip records the tray tooltip.
func (f *Fake) SetTooltip(tooltip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tooltip = tooltip
}

// Tooltip returns the tray tooltip last set.
func (f *Fake) Tooltip() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tooltip
}

// AddItem implements [Driver].
func (f *Fake) AddItem(parent Native, spec Spec) Native {
	item := &FakeItem{
		fake:    f,
		spec:    spec,
		title:   spec.Title,
		tooltip: spec.Tooltip,
		checked: spec.Checked,
		enabled: true,
		visible: true,
		clicks:  make(chan struct{}),
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendLocked(parent, item)
	return item
}

// AddSeparator implements [Driver].
func (f *Fake) AddSeparator(parent Native) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendLocked(parent, &FakeItem{fake: f, separator: true})
}

func (f *Fake) appendLocked(parent Native, item *FakeItem) {
	if parent == nil {
		f.children = append(f.children, item)
		return
	}
	p := parent.(*FakeItem)
	p.children = append(p.children, item)
}

// Items returns the top-level items, separators included, in menu order.
func (f *Fake) Items() []*FakeItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*FakeItem(nil), f.children...)
}

// Find returns the item reached by following titles down the menu, or nil.
// Titles are matched as they read now, so an item is found by what the user
// would see rather than by what it was created with.
func (f *Fake) Find(titles ...string) *FakeItem {
	f.mu.Lock()
	defer f.mu.Unlock()

	children := f.children
	var found *FakeItem
	for _, title := range titles {
		found = nil
		for _, child := range children {
			if !child.separator && child.title == title {
				found = child
				break
			}
		}
		if found == nil {
			return nil
		}
		children = found.children
	}
	return found
}

// Render draws the menu as text, one item per line, indented by depth. It is
// meant for assertions on shape and state:
//
//	Mode: Read/Write
//	  [x] Read/Write Mode
//	  [ ] Read Only Mode
//	----
//	Quit
//
// A checkbox is shown as [x] or [ ]; a disabled item is suffixed with
// "(disabled)" and a hidden one with "(hidden)".
func (f *Fake) Render() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var b strings.Builder
	f.renderLocked(&b, f.children, 0)
	return b.String()
}

func (f *Fake) renderLocked(b *strings.Builder, items []*FakeItem, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, item := range items {
		b.WriteString(indent)
		if item.separator {
			b.WriteString("----\n")
			continue
		}

		if item.spec.Checkbox {
			if item.checked {
				b.WriteString("[x] ")
			} else {
				b.WriteString("[ ] ")
			}
		}
		b.WriteString(item.title)
		if !item.enabled {
			b.WriteString(" (disabled)")
		}
		if !item.visible {
			b.WriteString(" (hidden)")
		}
		b.WriteString("\n")

		f.renderLocked(b, item.children, depth+1)
	}
}

// FakeItem is one item recorded by a [Fake]. Its getters report what the menu
// last pushed to the platform, which is what a real tray would be showing.
type FakeItem struct {
	fake      *Fake
	spec      Spec
	separator bool
	clicks    chan struct{}

	// Guarded by fake.mu, so a test reading state races nothing with the
	// menu's own goroutines.
	title    string
	tooltip  string
	checked  bool
	enabled  bool
	visible  bool
	children []*FakeItem
}

// SetTitle implements [Native].
func (i *FakeItem) SetTitle(title string) {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	i.title = title
}

// SetTooltip implements [Native].
func (i *FakeItem) SetTooltip(tooltip string) {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	i.tooltip = tooltip
}

// SetChecked implements [Native].
func (i *FakeItem) SetChecked(checked bool) {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	i.checked = checked
}

// SetEnabled implements [Native].
func (i *FakeItem) SetEnabled(enabled bool) {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	i.enabled = enabled
}

// SetVisible implements [Native].
func (i *FakeItem) SetVisible(visible bool) {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	i.visible = visible
}

// Clicks implements [Native]. Sending on the returned channel drives the same
// path a platform click takes, including the menu's watcher goroutine; prefer
// [Item.Click], which also waits for the handlers to finish.
func (i *FakeItem) Clicks() <-chan struct{} { return i.clicks }

// Deliver pushes a click through the click channel, exactly as a platform
// would. It returns once the menu's watcher has taken it, which is before the
// handlers have run — a test that needs to wait for those wants [Item.Click].
func (i *FakeItem) Deliver() { i.clicks <- struct{}{} }

// Title returns the item's current label.
func (i *FakeItem) Title() string {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return i.title
}

// Tooltip returns the item's current hover text.
func (i *FakeItem) Tooltip() string {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return i.tooltip
}

// Checked reports whether the item shows a checkmark.
func (i *FakeItem) Checked() bool {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return i.checked
}

// Enabled reports whether the item is clickable.
func (i *FakeItem) Enabled() bool {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return i.enabled
}

// Visible reports whether the item is on the menu.
func (i *FakeItem) Visible() bool {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return i.visible
}

// IsSeparator reports whether this entry is a divider rather than an item.
func (i *FakeItem) IsSeparator() bool { return i.separator }

// Children returns the item's submenu, in menu order.
func (i *FakeItem) Children() []*FakeItem {
	i.fake.mu.Lock()
	defer i.fake.mu.Unlock()
	return append([]*FakeItem(nil), i.children...)
}
