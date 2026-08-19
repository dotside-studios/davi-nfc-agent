package tray

import (
	"fmt"
	"log"

	"fyne.io/systray"
)

// deviceSlotCount bounds the paired devices shown. Systray items cannot be
// removed once created, so the pool is fixed and reused, as with origins.
const deviceSlotCount = 12

type deviceSlot struct {
	item *systray.MenuItem
	id   string
}

// setupDevicesMenu builds the Paired Devices submenu.
func (s *App) setupDevicesMenu() {
	s.mDevicesMenu = systray.AddMenuItem("Paired Devices", "Phones and readers that have paired with this agent")

	for i := 0; i < deviceSlotCount; i++ {
		item := s.mDevicesMenu.AddSubMenuItem("", "")
		item.Hide()
		s.deviceSlots = append(s.deviceSlots, &deviceSlot{item: item})
	}

	s.mRequirePaired = s.mDevicesMenu.AddSubMenuItemCheckbox(
		"Require pairing",
		"Admit only devices that have paired. The shared secret and loopback bypass stop admitting devices; browser consoles are unaffected.",
		false,
	)

	s.mRevokeAllDevices = s.mDevicesMenu.AddSubMenuItem(
		"Revoke all devices",
		"Every paired device must pair again. Use when the machine changes hands.",
	)

	s.refreshDevicesMenu()
}

// refreshDevicesMenu redraws the submenu from the registry.
func (s *App) refreshDevicesMenu() {
	if s.agent.Devices == nil || len(s.deviceSlots) == 0 {
		return
	}

	devices := s.agent.Devices.List()

	slot := 0
	for _, device := range devices {
		if slot >= len(s.deviceSlots) {
			break
		}
		item := s.deviceSlots[slot]
		item.id = device.ID

		label := device.Name
		if device.Platform != "" {
			label = fmt.Sprintf("%s (%s)", device.Name, device.Platform)
		}
		item.item.SetTitle(label)
		item.item.SetTooltip(fmt.Sprintf("Paired %s — click to revoke", device.PairedAt.Local().Format("2006-01-02 15:04")))
		item.item.Show()
		slot++
	}

	for ; slot < len(s.deviceSlots); slot++ {
		s.deviceSlots[slot].id = ""
		s.deviceSlots[slot].item.Hide()
	}

	if len(devices) == 0 {
		s.mDevicesMenu.SetTitle("Paired Devices (none)")
		s.mRevokeAllDevices.Hide()
	} else {
		s.mDevicesMenu.SetTitle(fmt.Sprintf("Paired Devices (%d)", len(devices)))
		s.mRevokeAllDevices.Show()
	}

	if s.agent.RequirePairedDevice {
		s.mRequirePaired.Check()
	} else {
		s.mRequirePaired.Uncheck()
	}
}

// handleRequirePaired toggles the paired-device requirement live, so it can be
// tried against a real device without restarting the agent.
func (s *App) handleRequirePaired() {
	on := !s.mRequirePaired.Checked()

	if on && s.agent.Devices != nil && s.agent.Devices.Count() == 0 {
		// Turning this on with nothing paired locks out every device, which is
		// unlikely to be what the operator meant from this menu.
		log.Printf("[systray] Not requiring pairing: no devices are paired, so every device would be refused")
		s.mRequirePaired.Uncheck()
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

// handleDeviceRevokeSelection polls the device slots, matching how the other
// dynamic menus are handled.
func (s *App) handleDeviceRevokeSelection() {
	if s.agent.Devices == nil {
		return
	}

	for _, slot := range s.deviceSlots {
		select {
		case <-slot.item.ClickedCh:
			if slot.id == "" {
				continue
			}
			if err := s.agent.Devices.Revoke(slot.id); err != nil {
				log.Printf("[systray] Failed to revoke device %s: %v", slot.id, err)
				continue
			}
			log.Printf("[systray] Revoked device %s", slot.id)
			s.refreshDevicesMenu()
		default:
		}
	}
}

// handleRevokeAllDevices clears the registry.
func (s *App) handleRevokeAllDevices() {
	if s.agent.Devices == nil {
		return
	}

	if err := s.agent.Devices.RevokeAll(); err != nil {
		log.Printf("[systray] Failed to revoke devices: %v", err)
		return
	}

	log.Printf("[systray] Revoked all paired devices")
	s.refreshDevicesMenu()
}

// startDeviceWatcher redraws the menu when a device pairs, so a completed
// pairing shows up without the operator reopening the menu.
func (s *App) startDeviceWatcher() {
	if s.agent.Devices == nil {
		return
	}
	s.agent.Devices.OnChange(s.refreshDevicesMenu)
}
