package traymenu

import (
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/signals"
)

// Radio is a set of checkbox items of which exactly one is ticked, keyed by a
// value of the caller's own type:
//
//	modes := traymenu.NewRadio[nfc.ReaderMode](menu.AddSubmenu("Mode"))
//	modes.Add(nfc.ModeReadWrite, "Read/Write")
//	modes.Add(nfc.ModeReadOnly, "Read Only")
//	modes.Set(nfc.ModeReadWrite)
//	modes.OnSelect(func(m nfc.ReaderMode) { reader.SetMode(m) })
//
// Keeping the checkmarks consistent is the whole point: the alternative, which
// every hand-rolled tray menu ends up writing, is a fan of Uncheck calls that
// has to be repeated everywhere the value can change.
type Radio[T comparable] struct {
	parent Container

	mu      sync.Mutex
	entries []radioEntry[T]
	current T
	chosen  bool

	selected signals.Signal[T]
}

type radioEntry[T comparable] struct {
	value T
	item  *Item
}

// NewRadio returns an empty radio group that adds its items to parent.
func NewRadio[T comparable](parent Container) *Radio[T] {
	return &Radio[T]{parent: parent}
}

// Add appends an option and returns its item, so it can still be relabelled or
// hidden like any other.
func (r *Radio[T]) Add(value T, title string, opts ...Option) *Item {
	item := r.parent.AddCheckbox(title, false, opts...)
	item.OnClick(func() { r.choose(value) })

	r.mu.Lock()
	r.entries = append(r.entries, radioEntry[T]{value: value, item: item})
	// A group whose value was set before its options existed still shows it.
	if r.chosen && r.current == value {
		item.SetChecked(true)
	}
	r.mu.Unlock()

	return item
}

// Set ticks the option for value and unticks the rest, without raising
// [Radio.Selected]. It is how a change made elsewhere — restored settings, a
// second window — is reflected back into the menu without looping into the
// handler that would apply it again.
func (r *Radio[T]) Set(value T) {
	r.mu.Lock()
	r.current = value
	r.chosen = true
	entries := append([]radioEntry[T](nil), r.entries...)
	r.mu.Unlock()

	for _, entry := range entries {
		entry.item.SetChecked(entry.value == value)
	}
}

// Value reports the selected value, and whether anything is selected yet.
func (r *Radio[T]) Value() (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.chosen
}

// Item returns the item added for value, or nil.
func (r *Radio[T]) Item(value T) *Item {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range r.entries {
		if entry.value == value {
			return entry.item
		}
	}
	return nil
}

// Selected is raised when an option is clicked, with the value it stands for.
// Clicking the option that is already ticked raises it too: the click means
// "make this so", and a caller that has drifted out of sync needs to hear it.
func (r *Radio[T]) Selected() *signals.Signal[T] { return &r.selected }

// OnSelect runs fn whenever an option is clicked.
func (r *Radio[T]) OnSelect(fn func(T)) *signals.Connection { return r.selected.Connect(fn) }

func (r *Radio[T]) choose(value T) {
	r.Set(value)
	r.selected.Emit(value)
}
