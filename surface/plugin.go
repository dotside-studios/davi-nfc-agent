package surface

import "strings"

// Info is what a plugin says about itself before it is attached. It is read
// once, when the plugin is registered, so a plugin cannot rename itself out of
// the slot it was given.
type Info struct {
	// ID keys the plugin in a registry and must be unique. Registering a
	// second plugin under an ID replaces the first, which is what makes
	// registration safe to repeat.
	ID string

	// Title is the label of the plugin's own menu. Empty falls back to the ID,
	// since an untitled menu is unclickable rather than merely unnamed.
	Title string

	// Tooltip is the hover text on that menu.
	Tooltip string
}

// Name is the label the plugin's menu goes by: its title, or its ID when it
// gave none.
func (i Info) Name() string {
	if title := strings.TrimSpace(i.Title); title != "" {
		return title
	}
	return i.ID
}

// Plugin is a feature that puts its own entries on the agent's surfaces.
//
// Attach is called once, on the goroutine building the menu, with everything
// the plugin gets from the agent. Whatever it adds there stays for the life of
// the process: it keeps its items and changes them as the agent moves, rather
// than being asked to draw itself again.
type Plugin interface {
	Describe() Info
	Attach(Host) error
}

// Detacher is the optional other half, for a plugin holding something that
// outlives its menu: a listener of its own, a goroutine, a file. It is called
// as the agent quits, after the tray has gone.
//
// A plugin that only owns menu items does not need it. Their items go with the
// menu.
type Detacher interface {
	Detach()
}
