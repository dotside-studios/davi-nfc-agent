package surface

import (
	"errors"
	"fmt"
	"sync"
)

// ErrNoID is returned for a plugin whose Info carries no ID. Everything else
// keys off it, so one without an ID could never be found, replaced or removed.
var ErrNoID = errors.New("surface: plugin has no ID")

// Registry is a set of plugins, in the order they were added and keyed by ID.
//
// The zero value is ready to use.
type Registry struct {
	mu    sync.Mutex
	order []string
	byID  map[string]Plugin
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Add registers a plugin. A plugin already under that ID is replaced, keeping
// the place it had, so registering the same feature twice is a no-op rather
// than two menus.
func (r *Registry) Add(plugin Plugin) error {
	if plugin == nil {
		return errors.New("surface: nil plugin")
	}

	info := plugin.Describe()
	if info.ID == "" {
		return fmt.Errorf("%w: %T", ErrNoID, plugin)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.byID == nil {
		r.byID = make(map[string]Plugin)
	}
	if _, ok := r.byID[info.ID]; !ok {
		r.order = append(r.order, info.ID)
	}
	r.byID[info.ID] = plugin
	return nil
}

// Get returns the plugin registered under id, and whether there was one.
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.byID[id]
	return plugin, ok
}

// Remove unregisters a plugin and reports whether there was one. It says
// nothing about a plugin already attached: what it put on the menu is its own,
// and stays until it is removed there.
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.byID[id]; !ok {
		return false
	}
	delete(r.byID, id)
	for i, key := range r.order {
		if key == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

// Plugins returns the registered plugins, in the order they were added.
func (r *Registry) Plugins() []Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugins := make([]Plugin, 0, len(r.order))
	for _, id := range r.order {
		plugins = append(plugins, r.byID[id])
	}
	return plugins
}

// Len reports how many plugins are registered.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

// defaultRegistry is where a consumer's own plugin lands. The agent takes it up
// at startup, along with the features it ships itself.
var defaultRegistry = NewRegistry()

// Default is the registry an out-of-tree plugin registers itself in.
func Default() *Registry { return defaultRegistry }

// Register adds a plugin to the default registry. It is meant for an init
// function in a consumer's own package, where a returned error has nowhere to
// go, so it panics on the one thing that can be wrong there: a plugin with no
// ID. Use [Registry.Add] anywhere a failure can be handled.
func Register(plugin Plugin) {
	if err := defaultRegistry.Add(plugin); err != nil {
		panic(err)
	}
}
