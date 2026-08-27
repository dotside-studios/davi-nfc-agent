package serverplugin

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// originSlotCount bounds the origins shown in the menu. The list is a handful
// of consoles in practice, and the pool is fixed and reused: a row added on a
// refresh would land after the allow-any toggle below it.
const originSlotCount = 8

// originRow is what one row of the Allowed Origins section stands for: the
// origin, and whether clicking it allows the origin rather than revoking it.
type originRow struct {
	origin  string
	blocked bool
}

// loadOrigins settles the allowlist this plugin serves behind, building one
// from the agent's config directory when it was given none.
func (p *Plugin) loadOrigins(ctx agent.AgentContext) {
	if p.Origins != nil {
		return
	}

	store, err := server.NewOriginStore(ctx.ConfigDir())
	if err != nil {
		p.failf("Failed to load the origin allowlist: %v", err)
		store, _ = server.NewOriginStore("")
	}

	for _, origin := range p.AllowedOrigins {
		if origin == "*" {
			p.logf("An allowed origin of \"*\" disables the origin check; any site the operator visits can drive the reader")
			store.SessionAllowAny(true)
			continue
		}
		if err := store.Allow(origin); err != nil {
			p.failf("Failed to allow origin %q: %v", origin, err)
		}
	}

	p.Origins = store
	p.republishOrigins()
}

// republishOrigins carries what the store reports onto the plugin's own signal,
// so a subscriber follows the plugin rather than the store it happens to hold.
// Called once, when the store settles.
func (p *Plugin) republishOrigins() {
	emit := func() { p.Events().Origins.Emit(p.OriginState()) }
	p.Origins.OnChange(emit)
	p.Origins.OnBlocked(func(string) { emit() })
}

// OriginState is the allowlist as something displaying it reads it. Empty
// before the plugin activates, when there is no store yet.
func (p *Plugin) OriginState() OriginState {
	if p == nil || p.Origins == nil {
		return OriginState{}
	}
	return OriginState{
		Allowed:       p.Origins.List(),
		Blocked:       p.Origins.Blocked(),
		CheckDisabled: p.Origins.IsSessionAllowAny(),
	}
}

// OnOriginsChange calls fn whenever the allowlist changes or refuses a
// connection. The connection it returns removes it.
//
// Deprecated: use Events().Origins, which also reports the current allowlist.
func (p *Plugin) OnOriginsChange(fn func()) *event.Connection {
	if p == nil {
		return nil
	}
	return p.Events().Origins.Signal.Connect(func(OriginState) { fn() })
}

// OriginPolicy is the allowlist as something serving connections reads it,
// resolved per call rather than when it is handed over. That is what lets a
// server built before this plugin activates be given it, and what makes an
// origin allowed while the agent runs take effect without anything being
// rebuilt.
//
// Give it to whatever else on this build admits a browser connection, so one
// allowlist answers for all of them.
func (p *Plugin) OriginPolicy() server.OriginPolicy { return pluginOrigins{plugin: p} }

// CheckOrigin admits or rejects an upgrade by Origin, for whatever serves a
// WebSocket endpoint beside this plugin, such as a device driver's.
func (p *Plugin) CheckOrigin() func(r *http.Request) bool {
	return server.CheckOriginPolicy(p.OriginPolicy())
}

// pluginOrigins reads the plugin's store when it is asked rather than when it
// is handed over. Before there is one nothing is on the list, which is what an
// agent that has not activated should answer.
type pluginOrigins struct{ plugin *Plugin }

func (o pluginOrigins) Allowed(origin string) bool {
	store := o.plugin.Origins
	return store != nil && store.Allowed(origin)
}

func (o pluginOrigins) RecordBlocked(origin string) {
	if store := o.plugin.Origins; store != nil {
		store.RecordBlocked(origin)
	}
}

// originsMenu builds the Allowed Origins section: what is permitted now, and
// what has been refused since the agent started, offered for one click.
func (p *Plugin) originsMenu(ctx agent.AgentContext) {
	section := ctx.Systray.Section("Allowed Origins",
		traymenu.Tooltip("Web pages permitted to use this reader"))

	p.origins = traymenu.NewList[originRow](section, originSlotCount, traymenu.Checkbox(false))
	p.origins.OnActivate(func(row traymenu.Row[originRow]) { p.toggleOrigin(row.Value) })

	p.originAllowAny = section.AddCheckbox(
		"Allow any origin (this session)",
		p.Origins.IsSessionAllowAny(),
		traymenu.Tooltip("Turns the origin check off until the agent restarts. Any site the operator visits can then read, write and permanently lock cards."),
		traymenu.OnClick(p.toggleAllowAny),
	)

	p.refreshOrigins()
	p.Origins.OnChange(p.refreshOrigins)
	p.Origins.OnBlocked(func(origin string) {
		p.logf("Blocked connection from %s: allow it under Allowed Origins to let it use the reader", origin)
		p.refreshOrigins()
	})
}

// refreshOrigins redraws the section from the store: allowed origins first,
// then anything refused since start, offered for one-click approval.
func (p *Plugin) refreshOrigins() {
	allowed := p.Origins.List()
	blocked := p.Origins.Blocked()

	rows := make([]traymenu.Row[originRow], 0, len(allowed)+len(blocked))
	for _, origin := range allowed {
		rows = append(rows, traymenu.Row[originRow]{
			Value:   originRow{origin: origin},
			Title:   origin,
			Tooltip: "Allowed, click to revoke",
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

	if dropped := p.origins.Set(rows); dropped > 0 {
		p.logf("%d more origins than the menu can show; manage them from the control center", dropped)
	}

	p.originAllowAny.SetChecked(p.Origins.IsSessionAllowAny())
}

// toggleOrigin allows an origin that was refused, or revokes one that was
// allowed, depending on what the row was offering.
func (p *Plugin) toggleOrigin(row originRow) {
	if row.blocked {
		if err := p.Origins.Allow(row.origin); err != nil {
			p.failf("Failed to allow origin %q: %v", row.origin, err)
			return
		}
		p.logf("Allowed origin: %s", row.origin)
		return
	}

	if err := p.Origins.Revoke(row.origin); err != nil {
		p.failf("Failed to revoke origin %q: %v", row.origin, err)
		return
	}
	p.logf("Revoked origin: %s", row.origin)
}

// toggleAllowAny turns the origin check off for this run, or back on.
func (p *Plugin) toggleAllowAny() {
	on := !p.Origins.IsSessionAllowAny()
	p.Origins.SessionAllowAny(on)

	if on {
		p.logf("Origin check disabled for this session: any site can drive the reader until restart")
	} else {
		p.logf("Origin check re-enabled")
	}

	p.refreshOrigins()
}
