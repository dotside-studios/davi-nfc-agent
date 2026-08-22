package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pluginSlotCount is how many top-level menus are held open for plugins.
//
// No platform can insert a menu item in the middle, so a menu added once the
// tray is built lands at the end, under Quit. The slots are declared where a
// feature's menu belongs — beside the agent's own — and stay hidden until one
// takes it, which is what lets a plugin attach at any time and still read as
// part of the agent rather than as an afterthought below the exit.
const pluginSlotCount = 8

// reservePluginSlots declares those menus, in the place they belong.
func (s *App) reservePluginSlots() {
	for i := 0; i < pluginSlotCount; i++ {
		s.pluginSlots = append(s.pluginSlots, s.menu.Add("", traymenu.Hidden()))
	}
}

// The tray is the agent's [plugin.UI]: what a plugin fills a menu through, and
// what its copies and its links go through.

// MenuFor hands a plugin the menu it fills. It is called once per plugin, and
// only for a plugin that asks for a menu: one that only serves something never
// does, and leaves no empty menu behind reading as a feature that does
// nothing.
func (s *App) MenuFor(info plugin.Info) traymenu.Container {
	s.pluginMu.Lock()
	var item *traymenu.Item
	if s.pluginsTaken < len(s.pluginSlots) {
		item = s.pluginSlots[s.pluginsTaken]
		s.pluginsTaken++
	}
	s.pluginMu.Unlock()

	if item == nil {
		// More menus than the region holds. This one lands at the end, under
		// Quit, which reads badly but is better than a feature with nowhere to
		// put itself.
		log.Printf("[systray] The %s menu is below Quit: more than %d plugin menus are on the tray",
			info.Name(), pluginSlotCount)
		item = s.menu.Add("", traymenu.Hidden())
	}

	item.SetTitle(info.Name())
	item.SetTooltip(info.Tooltip)
	item.Show()
	return item
}

// Copy puts a value on the clipboard, and says so in the log — which is the
// only feedback a tray menu has for a copy.
func (s *App) Copy(what, value string) { copyValue(what, value) }

// Open shows a URL in the operator's browser.
func (s *App) Open(target string) error { return openBrowser(target) }
