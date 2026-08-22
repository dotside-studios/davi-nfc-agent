package traymenu

import "sync"

// Row is one entry in a List. Value carries whatever the caller needs back when
// the row is clicked, so the handler does not have to look the row up again by
// its label.
type Row[T any] struct {
	Value   T
	Title   string
	Tooltip string
	Checked bool
}

// List is a menu section whose contents change at runtime: paired devices,
// allowed origins, the readers currently plugged in.
//
// No supported platform can remove a menu item once it is added, so a List
// takes a fixed pool of items up front and relabels and hides them as the
// contents change. That is a cap, not a resize: Set reports how many rows did
// not fit.
type List[T any] struct {
	mu    sync.Mutex
	items []*Item
	rows  []Row[T]
	shown int

	activated Signal[Row[T]]
}

// NewList adds size hidden items to parent and returns the list that drives
// them. The options apply to every item in the pool; pass Checkbox for rows
// that carry checkmarks.
func NewList[T any](parent Container, size int, opts ...Option) *List[T] {
	list := &List[T]{
		items: make([]*Item, 0, size),
		rows:  make([]Row[T], size),
	}

	for i := 0; i < size; i++ {
		slot := i
		item := parent.Add("", append([]Option{Hidden()}, opts...)...)
		item.OnClick(func() { list.activate(slot) })
		list.items = append(list.items, item)
	}

	return list
}

// Set replaces the contents of the list, and reports how many rows had to be
// dropped for want of a slot.
func (l *List[T]) Set(rows []Row[T]) (dropped int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	shown := len(rows)
	if shown > len(l.items) {
		dropped = shown - len(l.items)
		shown = len(l.items)
	}

	for i := 0; i < shown; i++ {
		row := rows[i]
		l.rows[i] = row

		item := l.items[i]
		item.SetTitle(row.Title)
		item.SetTooltip(row.Tooltip)
		item.SetChecked(row.Checked)
		item.Show()
	}

	for i := shown; i < len(l.items); i++ {
		l.rows[i] = Row[T]{}
		l.items[i].Hide()
	}

	l.shown = shown
	return dropped
}

// Clear empties the list.
func (l *List[T]) Clear() { l.Set(nil) }

// Len reports how many rows are showing.
func (l *List[T]) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.shown
}

// Cap reports how many rows the list can show at once.
func (l *List[T]) Cap() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// Rows returns the rows currently showing.
func (l *List[T]) Rows() []Row[T] {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Row[T](nil), l.rows[:l.shown]...)
}

// Items returns the pool backing the list, showing and hidden alike, for
// anything the Row fields do not cover. Titles and visibility belong to Set.
func (l *List[T]) Items() []*Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*Item(nil), l.items...)
}

// Activated is raised when a row is clicked, carrying the row as it was set.
func (l *List[T]) Activated() *Signal[Row[T]] { return &l.activated }

// OnActivate runs fn whenever a row is clicked.
func (l *List[T]) OnActivate(fn func(Row[T])) *Connection { return l.activated.Connect(fn) }

func (l *List[T]) activate(slot int) {
	l.mu.Lock()
	if slot >= l.shown {
		// The slot was emptied between the click and here, so the row it named
		// is already gone.
		l.mu.Unlock()
		return
	}
	row := l.rows[slot]
	l.mu.Unlock()

	// Emitted outside the lock: a handler that redraws the list calls straight
	// back into Set.
	l.activated.Emit(row)
}
