package plugin

import "strings"

// Info is what a plugin says about itself. It is read once, when the plugin is
// registered, so a plugin cannot rename itself out of the place it was given.
type Info struct {
	// ID keys the plugin and must be unique. It is what a peer looks it up by,
	// what its log lines are tagged with, and what registering twice replaces.
	ID string

	// Title is the label of the plugin's own menu. Empty falls back to the ID,
	// since an unlabelled menu is unusable rather than merely unnamed.
	Title string

	// Tooltip is the hover text on that menu.
	Tooltip string
}

// Name is the label the plugin goes by: its title, or its ID when it gave none.
func (i Info) Name() string {
	if title := strings.TrimSpace(i.Title); title != "" {
		return title
	}
	return i.ID
}

// Plugin is anything that plugs into the agent.
//
// Identity is all that is required. Every phase of the lifecycle is an optional
// interface, so a plugin implements what it has work in and nothing else.
type Plugin interface {
	Describe() Info
}

// Initer is a plugin with wiring to do before anything serves: a menu to fill,
// an address to declare, a peer to find. It runs once, in registration order,
// and a plugin that returns an error here is dropped — it is never started, and
// the rest carry on without it.
type Initer interface {
	Init(*Context) error
}

// Starter is a plugin that serves something. Start runs in registration order,
// and again after a restart, so it must be able to bring the same plugin up
// twice.
type Starter interface {
	Start(*Context) error
}

// Stopper is the inverse of Starter, in reverse registration order. A plugin is
// only stopped if it started.
type Stopper interface {
	Stop(*Context) error
}

// Closer is a plugin holding something that outlives serving: a file, a
// goroutine, a registry of its own. It runs once, on the way out, after Stop.
type Closer interface {
	Close(*Context) error
}
