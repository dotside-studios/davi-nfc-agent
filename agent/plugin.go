package agent

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Plugin is a part of the agent that is assembled into it rather than built
// into it: the listener and what it serves, a bridge to another system, a menu
// entry and the work behind it.
//
// Nothing is loaded at run time. A plugin is a value the program constructs and
// registers, so which plugins a build has is decided by what it imports, and a
// plugin left out takes its dependencies with it.
//
// Activate is the only entry point, and it runs once, before the agent starts.
// What a plugin wants to happen afterwards it registers there: a [Component]
// through [AgentContext.Use] for anything with a lifetime, an entry on
// [AgentContext.Systray] for anything with a menu. There is no Deactivate,
// since a component already covers stopping.
type Plugin interface {
	// Activate wires the plugin into the agent. An error aborts the agent's
	// start, naming the plugin.
	//
	// It runs with the agent's lifecycle held, so it must not call Start, Stop
	// or Use on the agent itself. Everything it needs is on ctx.
	Activate(ctx AgentContext) error
}

// Named is the optional half of Plugin. A plugin that implements it is called
// what it says; one that does not is called after its type.
//
// Worth implementing for a plugin a program registers more than one of, since
// two of a type are otherwise indistinguishable in an error.
type Named interface {
	Name() string
}

// PluginName reports what to call p in logs and errors.
func PluginName(p Plugin) string {
	if named, ok := p.(Named); ok {
		if name := named.Name(); name != "" {
			return name
		}
	}

	t := reflect.TypeOf(p)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return "plugin"
	}
	if t.Name() == "" {
		return t.String()
	}
	return t.Name()
}

// PluginSet is the agent's plugin list, reached through [Agent.Plugins]:
//
//	a.Plugins.Add(&ServerPlugin{Config: cfg, Endpoints: endpoints})
//
// Plugins are activated in the order they were added, so one that mounts a
// route belongs after the one publishing the listener it mounts on.
//
// The set closes when the agent activates it. Adding afterwards returns an
// error rather than being accepted and never activated.
type PluginSet struct {
	mu      sync.Mutex
	plugins []Plugin
	sealed  bool
}

// Add registers plugins, in order. Safe to call from any goroutine, and stops
// at the first one it will not take.
func (s *PluginSet) Add(plugins ...Plugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range plugins {
		if p == nil {
			return fmt.Errorf("agent: nil plugin")
		}
		if s.sealed {
			return fmt.Errorf("agent: cannot register plugin %q once the agent has activated", PluginName(p))
		}
		s.plugins = append(s.plugins, p)
	}
	return nil
}

// List returns the registered plugins, in activation order.
func (s *PluginSet) List() []Plugin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Plugin(nil), s.plugins...)
}

// Len reports how many plugins are registered.
func (s *PluginSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.plugins)
}

// Activated reports whether the set has been activated, after which nothing
// more can be added.
func (s *PluginSet) Activated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sealed
}

// seal closes the set and returns what it holds in one step, so a plugin
// registered from another plugin's Activate is refused rather than dropped.
func (s *PluginSet) seal() []Plugin {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed = true
	return append([]Plugin(nil), s.plugins...)
}

// AgentContext is what a plugin is handed when it is activated: the agent it is
// being wired into, the menu its entries go on, and what it takes to register
// the rest.
//
// It is passed by value and holds no state, so it is good for the length of
// Activate. What a plugin registers outlives the call; the context does not.
type AgentContext struct {
	// Agent is the agent being assembled. It is not running yet: read its
	// configuration, register hooks such as OnTag and OnStateChange, and leave
	// starting it to whoever owns it.
	Agent *Agent

	// Systray is where the plugin's menu entries go, which for the shipped
	// tray is the top level of its menu: a plugin's entry is not marked out
	// from one the tray declared itself. It is never nil, since an agent with
	// no tray hands over a menu that draws nothing, so a plugin can add its
	// entries without asking whether anyone is looking.
	//
	// A plugin with more than one entry should group them under a submenu of
	// its own: ctx.Systray.Section("Backups").
	Systray traymenu.Container
}

// Use registers components to start and stop with the agent, in the order the
// plugins were activated.
func (ctx AgentContext) Use(components ...Component) error {
	for _, c := range components {
		if err := ctx.Agent.useLocked(c); err != nil {
			return err
		}
	}
	return nil
}

// Mounter is whatever the agent is served from: a listener, or anything else a
// route can be put on. Narrow on purpose, so that publishing one says nothing
// about what kind of server it is.
type Mounter interface {
	Mount(pattern string, handler http.Handler) error
}

// Serve publishes what the agent is served from, so a plugin registered after
// this one can add a route with Mount.
//
// It mounts nothing itself. The agent's own routes are [Agent.Routes], and
// whoever serves them mounts them, ahead of anything of its own.
//
// One plugin publishes, before any other mounts a route. A second call is an
// error rather than a quiet replacement: what a route was mounted on is not
// something to swap underneath it.
func (ctx AgentContext) Serve(m Mounter) error {
	return ctx.Agent.serveLocked(m)
}

// Mount adds a route to whatever the agent is served from. Whoever mounts a
// route decides what stands in front of it: CORS and authentication are the
// mounter's, since the answer differs per route.
func (ctx AgentContext) Mount(pattern string, handler http.Handler) error {
	m := ctx.Agent.mounter
	if m == nil {
		return fmt.Errorf("agent: cannot mount %q: nothing has been published to serve it; register the plugin that serves one first", pattern)
	}
	return m.Mount(pattern, handler)
}

// Logger is the agent's log, which is what the control center displays.
func (ctx AgentContext) Logger() *log.Logger { return ctx.Agent.Logger() }

// Info is what this build calls itself, for a plugin that presents a name.
func (ctx AgentContext) Info() buildinfo.Info { return ctx.Agent.Info() }

// ConfigDir is where the agent persists its state. A plugin with state of its
// own belongs in a subdirectory of it rather than beside the agent's files.
func (ctx AgentContext) ConfigDir() string { return ctx.Agent.ConfigDir() }

// Settings is the persisted preference store, or nil when the agent has none.
func (ctx AgentContext) Settings() *settings.Store { return ctx.Agent.SettingsStore() }

// Logs is the ring the agent's log is captured in, or nil when the program
// installed none. It is what a plugin serving a log view reads from.
func (ctx AgentContext) Logs() *logbuf.Ring { return ctx.Agent.Logs() }

// Activate runs the registered plugins, in order, hanging their menu entries on
// systray. A nil systray means there is no tray: the entries go to a menu that
// draws nothing.
//
// A host with a tray calls this itself, from inside the menu it is declaring:
// an item always goes to the end of its parent, so where the host activates the
// plugins is where their entries land. Start calls it if nothing else has.
//
// It happens once. Later calls report what the first decided, error included: a
// plugin that failed is not tried again over the ones already registered.
func (a *Agent) Activate(systray traymenu.Container) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.activateLocked(systray)
}

// Activated reports whether the plugins have been activated.
func (a *Agent) Activated() bool { return a.Plugins.Activated() }

// activateLocked is Activate with the lifecycle already held, which is how
// Start uses it: the plugins register their components and mount their routes
// before anything is opened or bound.
func (a *Agent) activateLocked(systray traymenu.Container) error {
	if a.Plugins.Activated() {
		return a.activateErr
	}

	plugins := a.Plugins.seal()
	if systray == nil {
		systray = a.discardMenu()
	}
	ctx := AgentContext{Agent: a, Systray: systray}

	for _, p := range plugins {
		name := PluginName(p)
		if err := p.Activate(ctx); err != nil {
			a.activateErr = fmt.Errorf("agent: plugin %q: %w", name, err)
			a.logger.Printf("Plugin %q failed to activate: %v", name, err)
			return a.activateErr
		}
		a.logger.Printf("Plugin activated: %s", name)
	}
	return nil
}

// discardMenu is the menu a plugin's entries go on when no host supplied one.
// Built on first use, so a headless agent pays for the goroutine behind it only
// if a plugin asks for a menu.
func (a *Agent) discardMenu() traymenu.Container {
	if a.trayMenu == nil {
		a.trayMenu = traymenu.New(traymenu.Discard())
	}
	return a.trayMenu
}

// serveLocked records what the agent is served from.
func (a *Agent) serveLocked(m Mounter) error {
	if m == nil {
		return fmt.Errorf("agent: nil mounter")
	}
	if a.mounter != nil {
		if a.mounter == m {
			return nil
		}
		return fmt.Errorf("agent: the agent is already being served")
	}

	a.mounter = m
	return nil
}
