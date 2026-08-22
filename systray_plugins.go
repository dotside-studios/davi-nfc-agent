package main

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pluginSlotCount is how many top-level menus are held open for plugins.
//
// No platform can insert a menu item in the middle, so a menu added once the
// tray is built lands at the end, under Quit. The slots are declared where a
// feature's menu belongs and stay hidden until one takes it, which is what lets
// a plugin attach at any time and still read as part of the agent rather than
// as an afterthought below the exit.
const pluginSlotCount = 8

// AttachPlugins gives the tray the plugins to attach when it builds the menu.
// Call it before Run.
func (s *SystrayApp) AttachPlugins(registry *surface.Registry) { s.plugins = registry }

// reservePluginSlots declares the menus plugins fill, in the place they belong.
func (s *SystrayApp) reservePluginSlots() {
	for i := 0; i < pluginSlotCount; i++ {
		s.pluginSlots = append(s.pluginSlots, s.menu.Add("", traymenu.Hidden()))
	}
}

// attachPlugins hands every registered plugin its host, in the order they were
// registered.
func (s *SystrayApp) attachPlugins() {
	if s.plugins == nil {
		return
	}

	for _, plugin := range s.plugins.Plugins() {
		s.AttachPlugin(plugin)
	}
}

// AttachPlugin attaches one plugin and reports whether it took. It may be
// called after the menu is built: the slots are reserved for exactly that.
//
// A plugin that fails to attach is not held on to, and anything it managed to
// add stays where it put it. There is no way to take a feature's menu back off
// the tray for it, and half a menu is more honest than a menu that lies about
// what is behind it.
func (s *SystrayApp) AttachPlugin(plugin surface.Plugin) bool {
	if plugin == nil {
		return false
	}
	info := plugin.Describe()

	host := &trayHost{app: s, info: info}
	if err := plugin.Attach(host); err != nil {
		log.Printf("[systray] The %s plugin did not attach: %v", info.Name(), err)
		return false
	}

	s.pluginMu.Lock()
	s.attached = append(s.attached, plugin)
	s.pluginMu.Unlock()

	log.Printf("[systray] Attached the %s plugin", info.Name())
	return true
}

// detachPlugins tells the plugins holding something of their own to let it go,
// on the way out. It runs before the agent stops, since a plugin may be using
// it.
func (s *SystrayApp) detachPlugins() {
	s.pluginMu.Lock()
	attached := append([]surface.Plugin(nil), s.attached...)
	s.attached = nil
	s.pluginMu.Unlock()

	for _, plugin := range attached {
		detacher, ok := plugin.(surface.Detacher)
		if !ok {
			continue
		}
		detacher.Detach()
	}
}

// takePluginSlot hands out the next reserved menu, titled for the plugin.
func (s *SystrayApp) takePluginSlot(info surface.Info) *traymenu.Item {
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
		log.Printf("[systray] The %s menu is below Quit: more than %d plugin menus are attached",
			info.Name(), pluginSlotCount)
		item = s.menu.Add("", traymenu.Hidden())
	}

	item.SetTitle(info.Name())
	item.SetTooltip(info.Tooltip)
	item.Show()
	return item
}

// State is the snapshot the plugins see, as of the last change reported.
func (s *SystrayApp) State() surface.State {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

// publishState takes a fresh snapshot and hands it to the plugins watching.
//
// It is called wherever the tray already redraws itself for a change, which is
// what makes a plugin reactive without polling: the agent starting or stopping,
// a card arriving or leaving, a settings change from either surface, a device
// pairing, a restart of the listeners.
func (s *SystrayApp) publishState() {
	// One publish at a time, so watchers see the changes in the order they
	// happened rather than racing two snapshots into the same menu.
	s.publishMu.Lock()
	defer s.publishMu.Unlock()

	state := s.snapshotState()

	s.stateMu.Lock()
	s.state = state
	s.stateMu.Unlock()

	s.stateChanged.Emit(state)
}

// snapshotState reads what the agent is doing now. Every value comes from the
// agent rather than from the menu, for the same reason the menus themselves are
// drawn from it: what a plugin acts on should be what the agent will do, not
// what a checkbox happens to show.
func (s *SystrayApp) snapshotState() surface.State {
	agent := s.agent

	state := surface.State{
		Running:  agent.Reader != nil,
		Device:   agent.CurrentDevicePath(),
		Port:     agent.ServingPort(),
		TLS:      agent.CertFile != "" && agent.KeyFile != "",
		Settings: agent.Settings(),
		Explicit: agent.Explicit(),
	}

	if agent.Devices != nil {
		state.Paired = agent.Devices.Count()
	}
	if agent.ClientServer != nil {
		if card := agent.ClientServer.GetLastCard(); card != nil && card.UID != "" {
			state.Card = surface.Card{Present: true, UID: card.UID, Type: card.Type}
		}
	}
	return state
}
