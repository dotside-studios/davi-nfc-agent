package tray

import (
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pairedSlotCount bounds the paired devices shown. The pool is fixed and
// reused, as with origins: a row added on a refresh would land after the
// actions below it.
const pairedSlotCount = 12

// setupDevicesMenu builds the Paired Devices submenu.
func (s *App) setupDevicesMenu() {
	s.mDevicesMenu = s.menu.AddSubmenu("Paired Devices",
		traymenu.Tooltip("Phones and readers that have paired with this agent"))

	// Declared first so the devices sit above the actions that apply to all of
	// them: menu order is the order items are added.
	s.pairedDevices = traymenu.NewList[string](s.mDevicesMenu, pairedSlotCount)
	s.pairedDevices.OnActivate(func(row traymenu.Row[string]) { s.revokeDevice(row.Value) })

	s.mRequirePaired = s.mDevicesMenu.AddCheckbox(
		"Require pairing",
		false,
		traymenu.Tooltip("Admit only devices that have paired. The shared secret and loopback bypass stop admitting devices; browser consoles are unaffected."),
		traymenu.OnClick(s.handleRequirePaired),
	)

	s.mRevokeAllDevices = s.mDevicesMenu.Add(
		"Revoke all devices",
		traymenu.Tooltip("Every paired device must pair again. Use when the machine changes hands."),
		traymenu.OnClick(s.handleRevokeAllDevices),
	)

	s.refreshDevicesMenu()
}

// refreshDevicesMenu redraws the submenu from the registry.
func (s *App) refreshDevicesMenu() {
	if s.agent.Devices() == nil || s.pairedDevices == nil {
		return
	}

	devices := s.agent.Devices().List()

	rows := make([]traymenu.Row[string], 0, len(devices))
	for _, device := range devices {
		label := device.Name
		if device.Platform != "" {
			label = fmt.Sprintf("%s (%s)", device.Name, device.Platform)
		}
		rows = append(rows, traymenu.Row[string]{
			Value:   device.ID,
			Title:   label,
			Tooltip: fmt.Sprintf("Paired %s — click to revoke", device.PairedAt.Local().Format("2006-01-02 15:04")),
		})
	}

	if dropped := s.pairedDevices.Set(rows); dropped > 0 {
		log.Printf("[systray] %d more devices are paired than the menu can show; revoke from the control center", dropped)
	}

	if len(devices) == 0 {
		s.mDevicesMenu.SetTitle("Paired Devices (none)")
		s.mRevokeAllDevices.Hide()
	} else {
		s.mDevicesMenu.SetTitle(fmt.Sprintf("Paired Devices (%d)", len(devices)))
		s.mRevokeAllDevices.Show()
	}

	s.mRequirePaired.SetChecked(s.agent.RequirePairedDevice())
}

// handleRequirePaired toggles the paired-device requirement live, so it can be
// tried against a real device without restarting the agent.
func (s *App) handleRequirePaired() {
	on := !s.mRequirePaired.Checked()

	if on && s.agent.Devices() != nil && s.agent.Devices().Count() == 0 {
		// Turning this on with nothing paired locks out every device, which is
		// unlikely to be what the operator meant from this menu.
		log.Printf("[systray] Not requiring pairing: no devices are paired, so every device would be refused")
		s.mRequirePaired.SetChecked(false)
		return
	}

	s.agent.SetRequirePairedDevice(on)

	if on {
		log.Printf("[systray] Requiring paired devices; the shared secret no longer admits one")
	} else {
		log.Printf("[systray] No longer requiring paired devices")
	}

	s.refreshDevicesMenu()
}

// revokeDevice drops one paired device, which then has to pair again.
func (s *App) revokeDevice(id string) {
	if s.agent.Devices() == nil || id == "" {
		return
	}

	if err := s.agent.Devices().Revoke(id); err != nil {
		log.Printf("[systray] Failed to revoke device %s: %v", id, err)
		return
	}

	log.Printf("[systray] Revoked device %s", id)
	s.refreshDevicesMenu()
}

// handleRevokeAllDevices clears the registry.
func (s *App) handleRevokeAllDevices() {
	if s.agent.Devices() == nil {
		return
	}

	if err := s.agent.Devices().RevokeAll(); err != nil {
		log.Printf("[systray] Failed to revoke devices: %v", err)
		return
	}

	log.Printf("[systray] Revoked all paired devices")
	s.refreshDevicesMenu()
}

// startDeviceWatcher redraws the menu when a device pairs, so a completed
// pairing shows up without the operator reopening the menu.
func (s *App) startDeviceWatcher() {
	if s.agent.Devices() == nil {
		return
	}
	s.agent.Devices().OnChange(s.refreshDevicesMenu)
}
