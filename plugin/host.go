package plugin

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// ErrNoID is returned for a plugin whose Info carries no ID. Everything keys
// off it, so one without an ID could never be looked up, replaced or removed.
var ErrNoID = errors.New("plugin: plugin has no ID")

// Menus hands a plugin the menu it fills. The tray implements it; a build with
// no tray leaves [Config.Menus] nil, and a plugin's menu is discarded rather
// than the plugin having to know whether anything draws one.
type Menus interface {
	MenuFor(Info) traymenu.Container
}

// Config assembles a Host. Every field is optional: a host with none of them
// still runs plugins, they just have nowhere to put a menu, a copy or a line of
// log.
type Config struct {
	// Logf is where plugins' log lines go, tagged with the plugin's ID. Nil
	// means the standard logger.
	Logf func(format string, args ...any)

	// Menus hands out the menus plugins fill. Nil discards them.
	Menus Menus

	// Clipboard is what Context.Copy does, and Browser what Context.Open does.
	// Nil reports that this build cannot.
	Clipboard func(what, value string)
	Browser   func(target string) error
}

// Host is the agent's plugin runtime: what is registered, what phase each one
// is in, and everything they are handed.
//
// It is safe to use from any goroutine. Lifecycle calls run one at a time, and
// a plugin must not drive the lifecycle from inside one of its own phases.
type Host struct {
	config Config

	// runMu serializes the phases, so a Restart cannot interleave with a Stop.
	runMu sync.Mutex

	mu      sync.Mutex
	order   []string
	entries map[string]*entry
	menus   Menus
	inited  bool
	started bool

	endpoints Endpoints

	// publishMu keeps snapshots in the order they were taken; stateMu guards
	// the last one, so a plugin can read it from inside a handler.
	publishMu sync.Mutex
	stateMu   sync.Mutex
	state     State
	changed   traymenu.Signal[State]
}

// entry is one registered plugin and the phase it has reached.
type entry struct {
	plugin  Plugin
	info    Info
	ctx     *Context
	inited  bool
	running bool
}

// New returns a host with nothing registered.
func New(config Config) *Host {
	if config.Logf == nil {
		config.Logf = log.Printf
	}
	return &Host{config: config, menus: config.Menus, entries: make(map[string]*entry)}
}

// SetMenus gives the host somewhere to put plugin menus, for the usual order of
// events: the runtime is built with the agent, the tray only exists once the
// desktop says so.
func (h *Host) SetMenus(menus Menus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.menus = menus
}

// Use registers plugins, in the order they are given.
//
// Registering a second plugin under an ID replaces the first, which is stopped
// and closed on its way out. A plugin registered after the host is already
// running joins it where it is: it is wired up and started at once, rather than
// waiting for a restart.
func (h *Host) Use(plugins ...Plugin) error {
	var errs []error
	for _, plugin := range plugins {
		if err := h.add(plugin); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *Host) add(plugin Plugin) error {
	if plugin == nil {
		return errNilPlugin
	}
	info := plugin.Describe()
	if info.ID == "" {
		return fmt.Errorf("%w: %T", ErrNoID, plugin)
	}

	h.runMu.Lock()
	defer h.runMu.Unlock()

	h.mu.Lock()
	replaced := h.entries[info.ID]
	if replaced == nil {
		h.order = append(h.order, info.ID)
	}
	fresh := &entry{plugin: plugin, info: info}
	fresh.ctx = &Context{host: h, info: info}
	h.entries[info.ID] = fresh
	inited, started := h.inited, h.started
	h.mu.Unlock()

	if replaced != nil {
		h.retire(replaced)
	}

	if inited {
		if err := h.initOne(fresh); err != nil {
			return err
		}
	}
	if started {
		return h.startOne(fresh)
	}
	return nil
}

// Init wires up every plugin that has not been wired yet, in registration
// order. A plugin that fails is dropped: it is never started, and the rest
// carry on without it.
func (h *Host) Init() error {
	h.runMu.Lock()
	defer h.runMu.Unlock()

	h.mu.Lock()
	h.inited = true
	h.mu.Unlock()

	var errs []error
	for _, e := range h.snapshot() {
		if e.inited {
			continue
		}
		if err := h.initOne(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Start brings up everything registered, in registration order, wiring up
// anything Init has not reached yet: a host that is started without being
// wired up first still runs, rather than quietly running nothing.
//
// A plugin that fails to start stays registered, so a later Restart can try it
// again.
func (h *Host) Start() error {
	h.runMu.Lock()
	defer h.runMu.Unlock()

	h.mu.Lock()
	h.inited, h.started = true, true
	h.mu.Unlock()

	var errs []error
	for _, e := range h.snapshot() {
		if !e.inited {
			if err := h.initOne(e); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := h.startOne(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Stop takes down everything that started, in reverse registration order, so a
// plugin is stopped before whatever it was registered after.
func (h *Host) Stop() error {
	h.runMu.Lock()
	defer h.runMu.Unlock()

	h.mu.Lock()
	h.started = false
	h.mu.Unlock()

	return h.stopAll(h.snapshot())
}

// Restart takes the named plugins down and brings them back up, or all of them
// when none are named. It is what a change they depend on calls: a reissued
// certificate, a rotated secret, a port that moved.
func (h *Host) Restart(ids ...string) error {
	h.runMu.Lock()
	defer h.runMu.Unlock()

	entries := h.snapshot()
	if len(ids) > 0 {
		wanted := make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}

		var picked []*entry
		for _, e := range entries {
			if wanted[e.info.ID] {
				picked = append(picked, e)
			}
		}
		entries = picked
	}

	var errs []error
	if err := h.stopAll(entries); err != nil {
		errs = append(errs, err)
	}
	for _, e := range entries {
		if err := h.startOne(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close stops everything and releases what outlives serving, in reverse
// registration order. The host is done afterwards.
func (h *Host) Close() error {
	h.runMu.Lock()
	defer h.runMu.Unlock()

	h.mu.Lock()
	h.started = false
	h.mu.Unlock()

	entries := h.snapshot()

	var errs []error
	if err := h.stopAll(entries); err != nil {
		errs = append(errs, err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if err := h.closeOne(entries[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Running reports whether the host has been started and not stopped since.
func (h *Host) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

// Plugins returns what is registered, in registration order.
func (h *Host) Plugins() []Plugin {
	entries := h.snapshot()

	plugins := make([]Plugin, 0, len(entries))
	for _, e := range entries {
		plugins = append(plugins, e.plugin)
	}
	return plugins
}

// Lookup returns the plugin registered under id, and whether there was one. It
// is how one plugin reaches another it knows by name.
func (h *Host) Lookup(id string) (Plugin, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	e, ok := h.entries[id]
	if !ok {
		return nil, false
	}
	return e.plugin, true
}

// Find returns the first registered plugin that implements T. It is how a
// plugin reaches a capability rather than a name: whatever is serving HTTP,
// whatever knows about pairing.
//
//	if servers, ok := plugin.Find[interface{ Port() int }](host); ok { ... }
func Find[T any](h *Host) (T, bool) {
	for _, p := range h.Plugins() {
		if match, ok := p.(T); ok {
			return match, true
		}
	}

	var zero T
	return zero, false
}

// Endpoints is the register of addresses the agent hands out. Plugins publish
// theirs here and whatever draws the agent's menus reads it back, so an address
// appears without anything being told what it is.
func (h *Host) Endpoints() *Endpoints { return &h.endpoints }

// Routes returns every route the registered plugins want served, in
// registration order. Whatever is serving the agent's port asks for these as it
// comes up.
func (h *Host) Routes() []Route {
	var routes []Route
	for _, e := range h.snapshot() {
		provider, ok := e.plugin.(RouteProvider)
		if !ok {
			continue
		}
		for _, route := range provider.Routes() {
			if route.Pattern == "" || route.Handler == nil {
				h.config.Logf("[plugin] %s asked for a route with no %s", e.info.Name(), missingPart(route))
				continue
			}
			route.Owner = e.info.ID
			routes = append(routes, route)
		}
	}
	return routes
}

func missingPart(route Route) string {
	if route.Pattern == "" {
		return "pattern"
	}
	return "handler"
}

// State is the snapshot as of the last change reported.
func (h *Host) State() State {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.state
}

// Publish hands a fresh snapshot to everything watching. The agent calls it;
// plugins read it.
func (h *Host) Publish(state State) {
	// One at a time, so watchers see the changes in the order they happened
	// rather than racing two snapshots into the same menu.
	h.publishMu.Lock()
	defer h.publishMu.Unlock()

	h.stateMu.Lock()
	h.state = state
	h.stateMu.Unlock()

	h.changed.Emit(state)
}

// Changed is raised on every Publish.
func (h *Host) Changed() *traymenu.Signal[State] { return &h.changed }

// Watch runs fn whenever the state changes.
func (h *Host) Watch(fn func(State)) *traymenu.Connection { return h.changed.Connect(fn) }

// ---- phases ----

func (h *Host) initOne(e *entry) error {
	e.inited = true

	initer, ok := e.plugin.(Initer)
	if !ok {
		return nil
	}
	if err := initer.Init(e.ctx); err != nil {
		// Dropped rather than left half-wired: it is never started, and what it
		// managed to put on a menu stays where it put it. There is no taking a
		// feature's menu back for it, and half a menu is more honest than one
		// that lies about what is behind it.
		h.drop(e.info.ID)
		h.config.Logf("[plugin] %s did not start up and was left out: %v", e.info.Name(), err)
		return fmt.Errorf("plugin %s: %w", e.info.ID, err)
	}
	return nil
}

func (h *Host) startOne(e *entry) error {
	if e.running || !e.inited {
		return nil
	}

	starter, ok := e.plugin.(Starter)
	if !ok {
		e.running = true
		return nil
	}
	if err := starter.Start(e.ctx); err != nil {
		h.config.Logf("[plugin] %s did not start: %v", e.info.Name(), err)
		return fmt.Errorf("plugin %s: %w", e.info.ID, err)
	}

	e.running = true
	return nil
}

func (h *Host) stopAll(entries []*entry) error {
	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		if err := h.stopOne(entries[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *Host) stopOne(e *entry) error {
	if !e.running {
		return nil
	}
	e.running = false

	stopper, ok := e.plugin.(Stopper)
	if !ok {
		return nil
	}
	if err := stopper.Stop(e.ctx); err != nil {
		h.config.Logf("[plugin] %s did not stop cleanly: %v", e.info.Name(), err)
		return fmt.Errorf("plugin %s: %w", e.info.ID, err)
	}
	return nil
}

func (h *Host) closeOne(e *entry) error {
	closer, ok := e.plugin.(Closer)
	if !ok {
		return nil
	}
	if err := closer.Close(e.ctx); err != nil {
		h.config.Logf("[plugin] %s did not close cleanly: %v", e.info.Name(), err)
		return fmt.Errorf("plugin %s: %w", e.info.ID, err)
	}
	return nil
}

// retire takes a replaced plugin out of service, as far as it had got.
func (h *Host) retire(e *entry) {
	_ = h.stopOne(e)
	_ = h.closeOne(e)
}

// drop unregisters a plugin. What it put on a menu is its own and stays there.
func (h *Host) drop(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.entries, id)
	for i, key := range h.order {
		if key == id {
			h.order = append(h.order[:i], h.order[i+1:]...)
			return
		}
	}
}

// snapshot lists the entries in registration order. Phases run over a snapshot
// rather than the live map, so a plugin registering another from inside one is
// not iterating what it is changing.
func (h *Host) snapshot() []*entry {
	h.mu.Lock()
	defer h.mu.Unlock()

	entries := make([]*entry, 0, len(h.order))
	for _, id := range h.order {
		if e, ok := h.entries[id]; ok {
			entries = append(entries, e)
		}
	}
	return entries
}

// menuProvider is where a plugin's menu comes from, or nil when nothing draws
// one.
func (h *Host) menuProvider() Menus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.menus
}
