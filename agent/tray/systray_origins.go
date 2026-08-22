package tray

import (
	"log"

	"fyne.io/systray"
)

// originSlotCount bounds the origins shown in the menu. The list is a handful
// of consoles in practice, and systray items cannot be removed once created —
// only relabelled and hidden — so the pool is fixed and reused.
const originSlotCount = 8

// originSlot is one reusable row in the Allowed Origins submenu.
type originSlot struct {
	item   *systray.MenuItem
	origin string
	// blocked marks a slot offering an origin that was refused, so a click
	// allows it rather than revoking it.
	blocked bool
}

// setupOriginsMenu builds the Allowed Origins submenu.
func (s *App) setupOriginsMenu() {
	s.mOriginsMenu = systray.AddMenuItem("Allowed Origins", "Web pages permitted to use this reader")

	for i := 0; i < originSlotCount; i++ {
		item := s.mOriginsMenu.AddSubMenuItemCheckbox("", "", false)
		item.Hide()
		slot := &originSlot{item: item}
		s.originSlots = append(s.originSlots, slot)
		onClick(item, func() { s.toggleOriginInSlot(slot) })
	}

	s.mOriginAllowAny = s.mOriginsMenu.AddSubMenuItemCheckbox(
		"Allow any origin (this session)",
		"Turns the origin check off until the agent restarts. Any site the operator visits can then read, write and permanently lock cards.",
		false,
	)

	s.refreshOriginsMenu()
}

// refreshOriginsMenu redraws the submenu from the store: allowed origins first,
// then anything refused since start, offered for one-click approval.
func (s *App) refreshOriginsMenu() {
	if s.agent.Origins() == nil || len(s.originSlots) == 0 {
		return
	}

	allowed := s.agent.Origins().List()
	blocked := s.agent.Origins().Blocked()

	slot := 0
	for _, origin := range allowed {
		if slot >= len(s.originSlots) {
			break
		}
		s.setOriginSlot(s.originSlots[slot], origin, false)
		slot++
	}

	for _, origin := range blocked {
		if slot >= len(s.originSlots) {
			break
		}
		s.setOriginSlot(s.originSlots[slot], origin, true)
		slot++
	}

	for ; slot < len(s.originSlots); slot++ {
		s.menuMu.Lock()
		s.originSlots[slot].origin = ""
		s.menuMu.Unlock()
		s.originSlots[slot].item.Hide()
	}

	if s.agent.Origins().IsSessionAllowAny() {
		s.mOriginAllowAny.Check()
	} else {
		s.mOriginAllowAny.Uncheck()
	}
}

func (s *App) setOriginSlot(slot *originSlot, origin string, blocked bool) {
	s.menuMu.Lock()
	slot.origin = origin
	slot.blocked = blocked
	s.menuMu.Unlock()

	if blocked {
		slot.item.SetTitle("Allow " + origin)
		slot.item.SetTooltip("This page tried to use the reader and was refused")
		slot.item.Uncheck()
	} else {
		slot.item.SetTitle(origin)
		slot.item.SetTooltip("Allowed — click to revoke")
		slot.item.Check()
	}
	slot.item.Show()
}

// toggleOriginInSlot allows or revokes whichever origin this row is showing,
// read when the click arrives because the row is reused across refreshes.
func (s *App) toggleOriginInSlot(slot *originSlot) {
	if s.agent.Origins() == nil {
		return
	}

	s.menuMu.Lock()
	origin, blocked := slot.origin, slot.blocked
	s.menuMu.Unlock()

	if origin == "" {
		return
	}

	if blocked {
		if err := s.agent.Origins().Allow(origin); err != nil {
			log.Printf("[systray] Failed to allow origin %q: %v", origin, err)
			return
		}
		log.Printf("[systray] Allowed origin: %s", origin)
	} else {
		if err := s.agent.Origins().Revoke(origin); err != nil {
			log.Printf("[systray] Failed to revoke origin %q: %v", origin, err)
			return
		}
		log.Printf("[systray] Revoked origin: %s", origin)
	}
	s.refreshOriginsMenu()
}

// handleOriginAllowAny toggles the session-only escape hatch.
func (s *App) handleOriginAllowAny() {
	if s.agent.Origins() == nil {
		return
	}

	on := !s.mOriginAllowAny.Checked()
	s.agent.Origins().SessionAllowAny(on)

	if on {
		log.Printf("[systray] Origin check disabled for this session — any site can drive the reader until restart")
	} else {
		log.Printf("[systray] Origin check re-enabled")
	}

	s.refreshOriginsMenu()
}

// startOriginWatcher redraws the menu when an origin is refused, so a blocked
// console becomes a visible one-click prompt instead of a silent failure.
func (s *App) startOriginWatcher() {
	if s.agent.Origins() == nil {
		return
	}

	s.agent.Origins().OnBlocked(func(origin string) {
		log.Printf("[systray] Blocked connection from %s — allow it under Allowed Origins to let it use the reader", origin)
		s.refreshOriginsMenu()
	})
}
