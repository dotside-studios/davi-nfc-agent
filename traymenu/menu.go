package traymenu

import (
	"log"
	"sync"
)

// Container is something menu items can be added to: a Menu (the top level) or
// an Item (a submenu). The unexported methods keep implementations inside this
// package, since a container has to be backed by a real platform item.
type Container interface {
	// Add appends a plain item.
	Add(title string, opts ...Option) *Item
	// AddCheckbox appends an item that carries a checkmark.
	AddCheckbox(title string, checked bool, opts ...Option) *Item
	// AddSubmenu appends an item that other items can be added to. It is a
	// normal item otherwise: relabel, hide and click it like any other.
	AddSubmenu(title string, opts ...Option) *Item
	// AddSeparator appends a divider.
	AddSeparator()
	// Section appends a submenu that keyed entries are registered into, for
	// items added after the surrounding menu was built. See [NewSection].
	Section(title string, opts ...Option) *Section

	menu() *Menu
	native() Native
}

// Menu is the tray icon and the top level of its menu.
type Menu struct {
	driver Driver

	events    chan event
	done      chan struct{}
	closeOnce sync.Once

	mu   sync.Mutex
	logf func(format string, args ...any)
}

// event is one unit of work for the dispatch goroutine: a click to deliver, an
// acknowledgement to close once it has been, or both.
type event struct {
	item *Item
	done chan struct{}
}

// clickQueue is the dispatch backlog. It only fills when a handler blocks.
const clickQueue = 64

// New returns a menu drawn by driver. A nil driver draws nothing: see
// [Discard], and see traymenu/fynetray for the real tray.
//
// Nothing is shown until Run is called, and items may only be added once it has
// called back.
func New(driver Driver) *Menu {
	if driver == nil {
		driver = Discard()
	}

	m := &Menu{
		driver: driver,
		events: make(chan event, clickQueue),
		done:   make(chan struct{}),
		logf:   log.Printf,
	}
	go m.dispatch()
	return m
}

// SetLogger replaces where the menu reports a handler that panicked. A nil fn
// silences it.
func (m *Menu) SetLogger(fn func(format string, args ...any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logf = fn
}

// Run shows the tray icon, calls onReady once items may be added, and blocks
// until Quit is called. onExit runs before the menu stops dispatching clicks,
// so it can still touch the menu.
func (m *Menu) Run(onReady, onExit func()) {
	m.driver.Run(
		func() {
			if onReady != nil {
				onReady()
			}
		},
		func() {
			if onExit != nil {
				onExit()
			}
			m.Close()
		},
	)
}

// Quit tears the menu down, which returns from Run.
func (m *Menu) Quit() { m.driver.Quit() }

// Close stops delivering clicks and releases the goroutines behind the menu.
// Run does it on the way out; a menu built without Run needs it called. It is
// safe to call more than once.
func (m *Menu) Close() {
	m.closeOnce.Do(func() { close(m.done) })
}

// SetIcon sets the tray icon from encoded image bytes: PNG everywhere but
// Windows, which wants an ICO.
func (m *Menu) SetIcon(icon []byte) { m.driver.SetIcon(icon) }

// SetTooltip sets the text shown when hovering the tray icon.
func (m *Menu) SetTooltip(tooltip string) { m.driver.SetTooltip(tooltip) }

// Add appends a plain item to the top level.
func (m *Menu) Add(title string, opts ...Option) *Item {
	return m.add(nil, build(title, false, false, opts))
}

// AddCheckbox appends a checkbox item to the top level.
func (m *Menu) AddCheckbox(title string, checked bool, opts ...Option) *Item {
	return m.add(nil, build(title, true, checked, opts))
}

// AddSubmenu appends a submenu to the top level.
func (m *Menu) AddSubmenu(title string, opts ...Option) *Item {
	return m.add(nil, build(title, false, false, opts))
}

// AddSeparator appends a divider to the top level.
func (m *Menu) AddSeparator() { m.driver.AddSeparator(nil) }

// Section appends a keyed group of items to the top level. See [NewSection].
func (m *Menu) Section(title string, opts ...Option) *Section {
	return NewSection(m, title, opts...)
}

func (m *Menu) menu() *Menu    { return m }
func (m *Menu) native() Native { return nil }

// add creates the platform item and the Item that fronts it.
func (m *Menu) add(parent *Item, o options) *Item {
	var native Native
	if parent != nil {
		if parent.Removed() {
			// Nothing left to hang it on. An inert item is safer than one
			// under a parent the platform has already forgotten.
			return &Item{owner: m, title: o.spec.Title, tooltip: o.spec.Tooltip, removed: true}
		}
		native = parent.platform
	}

	item := &Item{
		owner:    m,
		platform: m.driver.AddItem(native, o.spec),
		title:    o.spec.Title,
		tooltip:  o.spec.Tooltip,
		checkbox: o.spec.Checkbox,
		checked:  o.spec.Checked,
		enabled:  true,
		visible:  true,
	}
	if parent != nil {
		parent.adopt(item)
	}

	// Applied after creation because that is what the platforms offer: an item
	// is created live and visible, then told otherwise.
	if !o.enabled {
		item.SetEnabled(false)
	}
	if !o.visible {
		item.SetVisible(false)
	}
	for _, fn := range o.onClick {
		item.OnClick(fn)
	}

	m.watch(item)
	return item
}

// watch keeps a receiver on the item's click channel for as long as the menu
// lives. Drivers drop clicks that nobody is waiting for, so this goroutine, not
// the handlers, is what makes a click reliable.
func (m *Menu) watch(item *Item) {
	clicks := item.platform.Clicks()
	if clicks == nil {
		return
	}

	go func() {
		for {
			select {
			case <-m.done:
				return
			case _, ok := <-clicks:
				if !ok {
					return
				}
				select {
				case m.events <- event{item: item}:
				case <-m.done:
					return
				}
			}
		}
	}()
}

// dispatch runs every click handler, one at a time and in arrival order.
//
// A closed done wins over a queued event. Selecting on the two together would
// pick between them at random whenever both are ready, so a click enqueued
// around a Close would run about half the time.
func (m *Menu) dispatch() {
	for {
		select {
		case <-m.done:
			return
		default:
		}

		select {
		case <-m.done:
			return
		case ev := <-m.events:
			select {
			case <-m.done:
				if ev.done != nil {
					close(ev.done)
				}
				return
			default:
			}

			if ev.item != nil {
				m.deliver(ev.item)
			}
			if ev.done != nil {
				close(ev.done)
			}
		}
	}
}

// deliver emits one click. A panicking handler is reported and swallowed
// rather than taking the tray down and every other menu item with it.
func (m *Menu) deliver(item *Item) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		m.mu.Lock()
		logf := m.logf
		m.mu.Unlock()
		if logf != nil {
			logf("traymenu: handler for %q panicked: %v", item.Title(), r)
		}
	}()

	item.clicked.Emit(item)
}
