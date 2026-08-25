package traymenu

import "sync"

// Checklist is a set of checkbox items of which any number can be ticked, keyed
// by a value of the caller's own type. It is the many-of counterpart to Radio.
//
// AddAll puts an entry at the top that stands for the whole set, ticked exactly
// when nothing else is, which is how a filter menu says "no filter":
//
//	types := traymenu.NewChecklist[string](menu.AddSubmenu("Card Type Filter"))
//	types.AddAll("All Types")
//	types.Add("NTAG213", "NTAG213")
//	types.Add("DESFire", "DESFire")
//
//	types.OnChange(func(picked []string) { agent.Filter(picked) })
type Checklist[T comparable] struct {
	parent Container

	mu      sync.Mutex
	all     *Item
	entries []checklistEntry[T]

	changed Signal[[]T]
}

type checklistEntry[T comparable] struct {
	value T
	item  *Item
}

// NewChecklist returns an empty checklist that adds its items to parent.
func NewChecklist[T comparable](parent Container) *Checklist[T] {
	return &Checklist[T]{parent: parent}
}

// AddAll adds the entry standing for the whole set. Clicking it unticks
// everything else, and it ticks itself whenever nothing else is ticked.
func (c *Checklist[T]) AddAll(title string, opts ...Option) *Item {
	item := c.parent.AddCheckbox(title, true, opts...)
	item.OnClick(c.clear)

	c.mu.Lock()
	c.all = item
	c.mu.Unlock()

	return item
}

// Add appends an entry and returns its item.
func (c *Checklist[T]) Add(value T, title string, opts ...Option) *Item {
	item := c.parent.AddCheckbox(title, false, opts...)
	item.OnClick(func() { c.toggle(value) })

	c.mu.Lock()
	c.entries = append(c.entries, checklistEntry[T]{value: value, item: item})
	c.mu.Unlock()

	return item
}

// Set ticks the entries for values and unticks the rest, without raising
// Changed. It reflects a selection made elsewhere, such as one from the console.
func (c *Checklist[T]) Set(values []T) {
	picked := make(map[T]bool, len(values))
	for _, value := range values {
		picked[value] = true
	}

	c.mu.Lock()
	entries := append([]checklistEntry[T](nil), c.entries...)
	all := c.all
	c.mu.Unlock()

	for _, entry := range entries {
		entry.item.SetChecked(picked[entry.value])
	}
	if all != nil {
		all.SetChecked(len(picked) == 0)
	}
}

// Values returns the ticked values, in the order their entries were added.
func (c *Checklist[T]) Values() []T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.valuesLocked()
}

// Item returns the item added for value, or nil.
func (c *Checklist[T]) Item(value T) *Item {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, entry := range c.entries {
		if entry.value == value {
			return entry.item
		}
	}
	return nil
}

// All returns the entry standing for the whole set, or nil if there is none.
func (c *Checklist[T]) All() *Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.all
}

// Changed is raised whenever the selection is clicked, carrying the ticked
// values. An empty slice means nothing is ticked, which the all entry shows.
func (c *Checklist[T]) Changed() *Signal[[]T] { return &c.changed }

// OnChange runs fn whenever the selection is clicked.
func (c *Checklist[T]) OnChange(fn func([]T)) *Connection { return c.changed.Connect(fn) }

func (c *Checklist[T]) toggle(value T) {
	c.mu.Lock()
	for _, entry := range c.entries {
		if entry.value == value {
			entry.item.Toggle()
			break
		}
	}
	values := c.valuesLocked()
	all := c.all
	c.mu.Unlock()

	if all != nil {
		all.SetChecked(len(values) == 0)
	}
	c.changed.Emit(values)
}

func (c *Checklist[T]) clear() {
	c.mu.Lock()
	entries := append([]checklistEntry[T](nil), c.entries...)
	all := c.all
	c.mu.Unlock()

	for _, entry := range entries {
		entry.item.SetChecked(false)
	}
	if all != nil {
		all.SetChecked(true)
	}
	c.changed.Emit(nil)
}

func (c *Checklist[T]) valuesLocked() []T {
	var values []T
	for _, entry := range c.entries {
		if entry.item.Checked() {
			values = append(values, entry.value)
		}
	}
	return values
}
