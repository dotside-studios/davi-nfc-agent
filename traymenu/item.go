package traymenu

import "sync"

// Item is one entry in a menu. It owns its state rather than reading it back
// from the platform, so Checked and friends mean the same thing on every
// driver.
type Item struct {
	owner    *Menu
	platform Native
	clicked  Signal[*Item]

	mu       sync.RWMutex
	children []*Item
	title    string
	tooltip  string
	checkbox bool
	checked  bool
	enabled  bool
	visible  bool
	removed  bool
}

// Clicked is the signal raised when the item is activated. OnClick is the
// shorthand for the common case.
func (i *Item) Clicked() *Signal[*Item] { return &i.clicked }

// OnClick runs fn whenever the item is activated. Handlers run in the order
// they were connected.
func (i *Item) OnClick(fn func()) *Connection {
	if fn == nil {
		return i.clicked.Connect(nil)
	}
	return i.clicked.Connect(func(*Item) { fn() })
}

// Click delivers a click as the platform would and blocks until the handlers
// have run. It is how a test drives a menu; a disabled or hidden item ignores
// it, as a real one would.
//
// Calling it from inside a handler deadlocks on the dispatch goroutine.
func (i *Item) Click() {
	i.mu.RLock()
	live := i.enabled && i.visible && !i.removed
	i.mu.RUnlock()
	if !live {
		return
	}

	done := make(chan struct{})
	select {
	case i.owner.events <- event{item: i, done: done}:
	case <-i.owner.done:
		return
	}

	select {
	case <-done:
	case <-i.owner.done:
	}
}

// Title returns the item's current label.
func (i *Item) Title() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.title
}

// SetTitle relabels the item.
func (i *Item) SetTitle(title string) {
	i.mu.Lock()
	changed := !i.removed && i.title != title
	if changed {
		i.title = title
	}
	i.mu.Unlock()

	if changed {
		i.platform.SetTitle(title)
	}
}

// Tooltip returns the item's current hover text.
func (i *Item) Tooltip() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.tooltip
}

// SetTooltip changes the item's hover text.
func (i *Item) SetTooltip(tooltip string) {
	i.mu.Lock()
	changed := !i.removed && i.tooltip != tooltip
	if changed {
		i.tooltip = tooltip
	}
	i.mu.Unlock()

	if changed {
		i.platform.SetTooltip(tooltip)
	}
}

// Checkbox reports whether the item was created as a checkbox.
func (i *Item) Checkbox() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.checkbox
}

// Checked reports whether the item currently carries a checkmark.
func (i *Item) Checked() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.checked
}

// SetChecked shows or hides the item's checkmark.
func (i *Item) SetChecked(checked bool) {
	i.mu.Lock()
	changed := !i.removed && i.checked != checked
	if changed {
		i.checked = checked
	}
	i.mu.Unlock()

	if changed {
		i.platform.SetChecked(checked)
	}
}

// Toggle flips the checkmark and reports its new state.
func (i *Item) Toggle() bool {
	i.mu.Lock()
	if i.removed {
		checked := i.checked
		i.mu.Unlock()
		return checked
	}
	next := !i.checked
	i.checked = next
	i.mu.Unlock()

	i.platform.SetChecked(next)
	return next
}

// Enabled reports whether the item can be clicked.
func (i *Item) Enabled() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.enabled
}

// SetEnabled greys the item out, or brings it back.
func (i *Item) SetEnabled(enabled bool) {
	i.mu.Lock()
	changed := !i.removed && i.enabled != enabled
	if changed {
		i.enabled = enabled
	}
	i.mu.Unlock()

	if changed {
		i.platform.SetEnabled(enabled)
	}
}

// Enable makes the item clickable again.
func (i *Item) Enable() { i.SetEnabled(true) }

// Disable greys the item out.
func (i *Item) Disable() { i.SetEnabled(false) }

// Visible reports whether the item is shown.
func (i *Item) Visible() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.visible
}

// SetVisible shows or hides the item.
func (i *Item) SetVisible(visible bool) {
	i.mu.Lock()
	changed := !i.removed && i.visible != visible
	if changed {
		i.visible = visible
	}
	i.mu.Unlock()

	if changed {
		i.platform.SetVisible(visible)
	}
}

// Show reveals a hidden item.
func (i *Item) Show() { i.SetVisible(true) }

// Hide takes the item off the menu, keeping its place for when it comes back.
func (i *Item) Hide() { i.SetVisible(false) }

// Removed reports whether the item has been taken off the menu for good.
func (i *Item) Removed() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.removed
}

// Remove takes the item off the menu for good, along with anything in its
// submenu, and disconnects its handlers. Every other method on it becomes a
// no-op: a removed item cannot be relabelled, shown or clicked again, and
// anything added to it comes back inert.
//
// Removing an item a List or Radio owns leaves that owner inconsistent; go
// through the owner instead. Use Hide for something that comes back.
func (i *Item) Remove() {
	if !i.markRemoved() {
		return
	}
	i.owner.driver.RemoveItem(i.platform)
}

// markRemoved marks the item and its submenu, and reports whether this call was
// the one that did it. Only the topmost item is handed to the driver, which
// takes the subtree with it; the rest have to be marked so that a state change
// on one cannot put it back on the menu.
func (i *Item) markRemoved() bool {
	i.mu.Lock()
	if i.removed {
		i.mu.Unlock()
		return false
	}
	i.removed = true
	children := append([]*Item(nil), i.children...)
	i.mu.Unlock()

	// Cleared before the driver is told: it may hand the watcher one last click
	// on the way out, and a removed item should not act on it.
	i.clicked.Clear()
	for _, child := range children {
		child.markRemoved()
	}
	return true
}

// adopt records a child so removal can reach it.
func (i *Item) adopt(child *Item) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.children = append(i.children, child)
}

// ---- Container ----

// Add appends a plain item to this item's submenu.
func (i *Item) Add(title string, opts ...Option) *Item {
	return i.owner.add(i, build(title, false, false, opts))
}

// AddCheckbox appends a checkbox item to this item's submenu.
func (i *Item) AddCheckbox(title string, checked bool, opts ...Option) *Item {
	return i.owner.add(i, build(title, true, checked, opts))
}

// AddSubmenu appends a nested submenu to this item's submenu.
func (i *Item) AddSubmenu(title string, opts ...Option) *Item {
	return i.owner.add(i, build(title, false, false, opts))
}

// AddSeparator appends a divider to this item's submenu.
func (i *Item) AddSeparator() {
	if i.Removed() {
		return
	}
	i.owner.driver.AddSeparator(i.platform)
}

// Section appends a keyed group of items to this item's submenu. See
// [NewSection].
func (i *Item) Section(title string, opts ...Option) *Section {
	return NewSection(i, title, opts...)
}

func (i *Item) menu() *Menu    { return i.owner }
func (i *Item) native() Native { return i.platform }

// ---- options ----

// options is what the Option functions accumulate before an item exists.
type options struct {
	spec    Spec
	enabled bool
	visible bool
	onClick []func()
}

// Option configures an item as it is added.
type Option func(*options)

func build(title string, checkbox, checked bool, opts []Option) options {
	o := options{
		spec:    Spec{Title: title, Checkbox: checkbox, Checked: checked},
		enabled: true,
		visible: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// Tooltip sets the item's hover text.
func Tooltip(tooltip string) Option {
	return func(o *options) { o.spec.Tooltip = tooltip }
}

// Checkbox makes the item a checkbox with the given initial state. Prefer
// AddCheckbox; this is for helpers that build items on the caller's behalf,
// such as NewList.
func Checkbox(checked bool) Option {
	return func(o *options) {
		o.spec.Checkbox = true
		o.spec.Checked = checked
	}
}

// Disabled greys the item out from the start, for labels that show a value
// rather than offering an action.
func Disabled() Option {
	return func(o *options) { o.enabled = false }
}

// Hidden keeps the item off the menu until something shows it.
func Hidden() Option {
	return func(o *options) { o.visible = false }
}

// HiddenIf hides the item when cond holds.
func HiddenIf(cond bool) Option {
	return func(o *options) {
		if cond {
			o.visible = false
		}
	}
}

// OnClick connects a click handler as the item is added.
func OnClick(fn func()) Option {
	return func(o *options) {
		if fn != nil {
			o.onClick = append(o.onClick, fn)
		}
	}
}
