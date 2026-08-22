package plugin

import (
	"errors"
	"sync"
)

// Registry is where a plugin waits to be picked up: a set of plugins keyed by
// ID, in the order they were added.
//
// It exists for the plugin a consumer registers from an init function, before
// there is a [Host] to register it in. The command line takes the default
// registry up at startup and hands what is in it to the host.
//
// The zero value is ready to use.
type Registry struct {
	mu      sync.Mutex
	order   []string
	entries map[string]Plugin
}

// Add registers a plugin, replacing anything already under its ID and keeping
// the place that one had.
func (r *Registry) Add(plugin Plugin) error {
	if plugin == nil {
		return errNilPlugin
	}
	info := plugin.Describe()
	if info.ID == "" {
		return ErrNoID
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.entries == nil {
		r.entries = make(map[string]Plugin)
	}
	if _, ok := r.entries[info.ID]; !ok {
		r.order = append(r.order, info.ID)
	}
	r.entries[info.ID] = plugin
	return nil
}

// Remove unregisters a plugin and reports whether there was one.
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.entries[id]; !ok {
		return false
	}
	delete(r.entries, id)
	for i, key := range r.order {
		if key == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

// Get returns the plugin registered under id, and whether there was one.
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.entries[id]
	return plugin, ok
}

// Plugins returns what is registered, in the order it was added.
func (r *Registry) Plugins() []Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugins := make([]Plugin, 0, len(r.order))
	for _, id := range r.order {
		plugins = append(plugins, r.entries[id])
	}
	return plugins
}

// Len reports how many plugins are registered.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

var (
	errNilPlugin    = errors.New("plugin: nil plugin")
	defaultRegistry = &Registry{}
)

// Default is the registry an out-of-tree plugin registers itself in.
func Default() *Registry { return defaultRegistry }

// Register adds a plugin to the default registry. It is meant for an init
// function in a consumer's own package, where a returned error has nowhere to
// go, so it panics on the one thing that can be wrong there: a plugin with no
// ID. Use [Registry.Add] or [Host.Use] anywhere a failure can be handled.
func Register(plugin Plugin) {
	if err := defaultRegistry.Add(plugin); err != nil {
		panic(err)
	}
}
