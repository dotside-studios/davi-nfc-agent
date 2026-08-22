package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// originSlotCount bounds the origins shown in the menu. The list is a handful
// of consoles in practice, and the pool is fixed and reused: a row added on a
// refresh would land after the allow-any toggle below it.
const originSlotCount = 8

// originRow is what one row of the Allowed Origins submenu stands for: the
// origin, and whether clicking it allows the origin rather than revoking it.
type originRow struct {
	origin  string
	blocked bool
}

// setupOriginsMenu builds the Allowed Origins submenu.
func (s *App) setupOriginsMenu() {
	origins := s.menu.AddSubmenu("Allowed Origins",
		traymenu.Tooltip("Web pages permitted to use this reader"))

	s.origins = traymenu.NewList[originRow](origins, originSlotCount, traymenu.Checkbox(false))
	s.origins.OnActivate(func(row traymenu.Row[originRow]) { s.toggleOrigin(row.Value) })

	s.mOriginAllowAny = origins.AddCheckbox(
		"Allow any origin (this session)",
		false,
		traymenu.Tooltip("Turns the origin check off until the agent restarts. Any site the operator visits can then read, write and permanently lock cards."),
		traymenu.OnClick(s.handleOriginAllowAny),
	)

	s.refreshOriginsMenu()
}

// refreshOriginsMenu redraws the submenu from the store: allowed origins first,
// then anything refused since start, offered for one-click approval.
func (s *App) refreshOriginsMenu() {
	if s.agent.Origins == nil || s.origins == nil {
		return
	}

	allowed := s.agent.Origins.List()
	blocked := s.agent.Origins.Blocked()

	rows := make([]traymenu.Row[originRow], 0, len(allowed)+len(blocked))
	for _, origin := range allowed {
		rows = append(rows, traymenu.Row[originRow]{
			Value:   originRow{origin: origin},
			Title:   origin,
			Tooltip: "Allowed — click to revoke",
			Checked: true,
		})
	}
	for _, origin := range blocked {
		rows = append(rows, traymenu.Row[originRow]{
			Value:   originRow{origin: origin, blocked: true},
			Title:   "Allow " + origin,
			Tooltip: "This page tried to use the reader and was refused",
		})
	}

	if dropped := s.origins.Set(rows); dropped > 0 {
		log.Printf("[systray] %d more origins than the menu can show; manage them from the control center", dropped)
	}

	s.mOriginAllowAny.SetChecked(s.agent.Origins.IsSessionAllowAny())
}

// toggleOrigin allows an origin that was refused, or revokes one that was
// allowed, depending on what the row was offering.
func (s *App) toggleOrigin(row originRow) {
	if s.agent.Origins == nil || row.origin == "" {
		return
	}

	if row.blocked {
		if err := s.agent.Origins.Allow(row.origin); err != nil {
			log.Printf("[systray] Failed to allow origin %q: %v", row.origin, err)
			return
		}
		log.Printf("[systray] Allowed origin: %s", row.origin)
	} else {
		if err := s.agent.Origins.Revoke(row.origin); err != nil {
			log.Printf("[systray] Failed to revoke origin %q: %v", row.origin, err)
			return
		}
		log.Printf("[systray] Revoked origin: %s", row.origin)
	}

	s.refreshOriginsMenu()
}

// handleOriginAllowAny toggles the session-only escape hatch.
func (s *App) handleOriginAllowAny() {
	if s.agent.Origins == nil {
		return
	}

	on := !s.mOriginAllowAny.Checked()
	s.agent.Origins.SessionAllowAny(on)

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
	if s.agent.Origins == nil {
		return
	}

	s.agent.Origins.OnBlocked(func(origin string) {
		log.Printf("[systray] Blocked connection from %s — allow it under Allowed Origins to let it use the reader", origin)
		s.refreshOriginsMenu()
	})
}
