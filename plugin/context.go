package plugin

import (
	"errors"
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Context is one plugin's view of the agent, handed to it in every phase. It is
// the whole of what a plugin can reach: there is no way through it to the tray
// itself, to another plugin's menu, or to the agent's settings.
//
// The same Context is passed to every phase of the same plugin, so anything it
// hands out — a menu, a watch — is the one it was given before.
type Context struct {
	host *Host
	info Info

	mu   sync.Mutex
	menu traymenu.Container
}

// Info is what the plugin said about itself.
func (c *Context) Info() Info { return c.info }

// Menu is the plugin's own menu, taken on first use.
//
// A plugin that only serves something never asks for one, and never leaves an
// empty menu behind reading as a feature that does nothing. A build with no
// tray discards what is put here, so a plugin needs no branch for it.
func (c *Context) Menu() traymenu.Container {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.menu == nil {
		menus := c.host.menuProvider()
		if menus == nil {
			c.menu = traymenu.Discard()
		} else {
			c.menu = menus.MenuFor(c.info)
		}
	}
	return c.menu
}

// State is what the agent is doing now.
func (c *Context) State() State { return c.host.State() }

// Watch runs fn whenever that changes.
//
// Handlers run one at a time, on whichever goroutine reported the change, so a
// slow one holds up the rest. Menu state may be set from any goroutine, so a
// handler normally does its work inline; anything that can wait on the OS
// belongs in a goroutine of its own.
func (c *Context) Watch(fn func(State)) *traymenu.Connection { return c.host.Watch(fn) }

// Peer returns another registered plugin, for a plugin that extends one it
// knows by name. Prefer [Find] for a capability rather than a name.
func (c *Context) Peer(id string) (Plugin, bool) { return c.host.Lookup(id) }

// Host is the runtime this plugin is registered in: where a plugin reaches its
// peers, with [Find] and [FindAll], and where one that registers another or
// drives a peer's lifecycle does it. A plugin must not call a lifecycle phase
// from inside one of its own.
func (c *Context) Host() *Host { return c.host }

// Copy puts a value on the clipboard. what names it for the log, which is the
// only feedback a tray menu has for a copy.
func (c *Context) Copy(what, value string) {
	clipboard := c.host.config.Clipboard
	if clipboard == nil {
		c.Logf("nothing to copy %s to in this build", what)
		return
	}
	clipboard(what, value)
}

// Open shows a URL in the operator's browser.
func (c *Context) Open(target string) error {
	browser := c.host.config.Browser
	if browser == nil {
		return errors.New("this build cannot open a browser")
	}
	return browser(target)
}

// Logf writes to the agent's log, tagged with the plugin's ID so a line says
// which feature wrote it. The plugin's own format string is expanded first: it
// is data here, not a format for this call.
func (c *Context) Logf(format string, args ...any) {
	c.host.config.Logf("[plugin:%s] %s", c.info.ID, fmt.Sprintf(format, args...))
}
