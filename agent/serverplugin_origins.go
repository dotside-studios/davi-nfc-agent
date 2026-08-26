package agent

import (
	"net/http"

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
func (p *ServerPlugin) loadOrigins(ctx AgentContext) {
	if p.Origins != nil {
		return
	}

	store, err := server.NewOriginStore(ctx.ConfigDir())
	if err != nil {
		p.logf("Failed to load the origin allowlist: %v", err)
		store, _ = server.NewOriginStore("")
	}

	for _, origin := range p.AllowedOrigins {
		if origin == "*" {
			p.logf("An allowed origin of \"*\" disables the origin check; any site the operator visits can drive the reader")
			store.SessionAllowAny(true)
			continue
		}
		if err := store.Allow(origin); err != nil {
			p.logf("Failed to allow origin %q: %v", origin, err)
		}
	}

	p.Origins = store
	for _, fn := range p.originWatchers {
		p.connectOrigins(fn)
	}
}

// OnOriginsChange calls fn whenever the allowlist changes or refuses a
// connection. It may be registered before activation, when there is no store
// yet: a console is built alongside this plugin rather than after it.
func (p *ServerPlugin) OnOriginsChange(fn func()) {
	p.originWatchers = append(p.originWatchers, fn)
	if p.Origins != nil {
		p.connectOrigins(fn)
	}
}

func (p *ServerPlugin) connectOrigins(fn func()) {
	p.Origins.OnChange(fn)
	p.Origins.OnBlocked(func(string) { fn() })
}

// OriginPolicy is the allowlist as something serving connections reads it,
// resolved per call rather than when it is handed over. That is what lets a
// server built before this plugin activates be given it, and what makes an
// origin allowed while the agent runs take effect without anything being
// rebuilt.
//
// Give it to whatever else on this build admits a browser connection, so one
// allowlist answers for all of them.
func (p *ServerPlugin) OriginPolicy() server.OriginPolicy { return pluginOrigins{plugin: p} }

// CheckOrigin admits or rejects an upgrade by Origin, for whatever serves a
// WebSocket endpoint beside this plugin, such as a device driver's.
func (p *ServerPlugin) CheckOrigin() func(r *http.Request) bool {
	return server.CheckOriginPolicy(p.OriginPolicy())
}

// pluginOrigins reads the plugin's store when it is asked rather than when it
// is handed over. Before there is one nothing is on the list, which is what an
// agent that has not activated should answer.
type pluginOrigins struct{ plugin *ServerPlugin }

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
func (p *ServerPlugin) originsMenu(ctx AgentContext) {
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
func (p *ServerPlugin) refreshOrigins() {
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
func (p *ServerPlugin) toggleOrigin(row originRow) {
	if row.blocked {
		if err := p.Origins.Allow(row.origin); err != nil {
			p.logf("Failed to allow origin %q: %v", row.origin, err)
			return
		}
		p.logf("Allowed origin: %s", row.origin)
		return
	}

	if err := p.Origins.Revoke(row.origin); err != nil {
		p.logf("Failed to revoke origin %q: %v", row.origin, err)
		return
	}
	p.logf("Revoked origin: %s", row.origin)
}

// toggleAllowAny turns the origin check off for this run, or back on.
func (p *ServerPlugin) toggleAllowAny() {
	on := !p.Origins.IsSessionAllowAny()
	p.Origins.SessionAllowAny(on)

	if on {
		p.logf("Origin check disabled for this session: any site can drive the reader until restart")
	} else {
		p.logf("Origin check re-enabled")
	}

	p.refreshOrigins()
}
