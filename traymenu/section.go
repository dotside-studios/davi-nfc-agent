package traymenu

import "sync"

// Section is a keyed group of items for entries registered after the menu is
// built, such as a plugin adding its own.
//
// New items go to the end of their parent, so anything registered late at the
// top level lands under Quit. A Section is a submenu declared where it belongs,
// which its entries then fill in from the end:
//
//	plugins := traymenu.NewSection(menu, "Plugins")
//	menu.AddSeparator()
//	menu.Add("Quit", traymenu.OnClick(menu.Quit))
//
//	plugins.Set("backup", "Back Up Now", traymenu.OnClick(backup.Run))
//
// Keys are the caller's own. Setting one that is already there replaces its
// item rather than adding a second, which makes registration safe to repeat.
type Section struct {
	item *Item

	mu      sync.Mutex
	entries map[string]*Item
	order   []string
}

// NewSection adds a submenu to parent and returns the section that fills it.
func NewSection(parent Container, title string, opts ...Option) *Section {
	return &Section{
		item:    parent.AddSubmenu(title, opts...),
		entries: make(map[string]*Item),
	}
}

// Item returns the submenu itself, to relabel or hide the section as a whole.
func (s *Section) Item() *Item { return s.item }

// Set registers an entry under key and returns its item. An entry already under
// that key is removed first, so the new one goes to the end of the section.
func (s *Section) Set(key, title string, opts ...Option) *Item {
	s.mu.Lock()
	existing, ok := s.entries[key]
	if ok {
		delete(s.entries, key)
		s.dropKeyLocked(key)
	}
	s.mu.Unlock()

	if ok {
		existing.Remove()
	}

	item := s.item.Add(title, opts...)

	s.mu.Lock()
	s.entries[key] = item
	s.order = append(s.order, key)
	s.mu.Unlock()

	return item
}

// Get returns the item registered under key, or nil.
func (s *Section) Get(key string) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[key]
}

// Remove takes the entry under key off the menu and reports whether there was
// one.
func (s *Section) Remove(key string) bool {
	s.mu.Lock()
	item, ok := s.entries[key]
	if ok {
		delete(s.entries, key)
		s.dropKeyLocked(key)
	}
	s.mu.Unlock()

	if !ok {
		return false
	}
	item.Remove()
	return true
}

// Clear empties the section.
func (s *Section) Clear() {
	s.mu.Lock()
	items := make([]*Item, 0, len(s.entries))
	for _, item := range s.entries {
		items = append(items, item)
	}
	s.entries = make(map[string]*Item)
	s.order = nil
	s.mu.Unlock()

	for _, item := range items {
		item.Remove()
	}
}

// Keys returns the registered keys, in the order their entries appear.
func (s *Section) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// Len reports how many entries are registered.
func (s *Section) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Section) dropKeyLocked(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}
