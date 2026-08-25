package agent

import (
	"log"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// TrustPlugin adds the tray entry that installs the local certificate
// authority, so browsers on this machine accept the agent, and hides it once
// there is nothing left to install, however the install was started.
//
// It is the only part of the certificate that is a plugin. A listener takes the
// certificate files and the reissue signal, and the pairing server the
// authority, each as the narrow type it needs.
//
//	trust := &agent.TrustPlugin{Manager: rt.Certificates}
//	rt.Agent.Plugins.Add(trust)
//
// Every method tolerates a nil plugin and a nil Manager, which is what a build
// serving a certificate it does not manage holds.
type TrustPlugin struct {
	// Manager is the certificate manager, or nil for a build whose certificate
	// comes from somewhere else. Setup builds one under the config directory.
	Manager *tlspkg.Manager

	// MenuTitle names the tray entry. Blank uses "Trust This Agent in Browsers".
	MenuTitle string

	entry  *traymenu.Item
	logger *log.Logger
}

var _ Plugin = (*TrustPlugin)(nil)

// Name identifies the plugin.
func (p *TrustPlugin) Name() string { return "certificates" }

// Activate adds the entry that installs the local authority. It sits beside the
// origin allowlist because the two are the pair of things a browser needs: the
// allowlist decides who may connect, this decides whether the connection can be
// opened at all.
func (p *TrustPlugin) Activate(ctx AgentContext) error {
	if p == nil || p.Manager == nil {
		return nil
	}
	p.logger = ctx.Logger()

	p.entry = ctx.Systray.Add(p.menuTitle(),
		traymenu.Tooltip("Install a local certificate authority so web pages on this machine can reach the reader"),
		traymenu.OnClick(p.install),
	)
	p.refresh()

	// Whether the authority is installed is a look at this machine, not a
	// decision taken once: a trust store that loses it needs the offer back
	// without an agent restart to notice.
	ctx.Events.Servers.Connect(func(int) { p.refresh() })
	return nil
}

// Manages reports whether this build manages its own certificate. False for one
// serving a certificate provisioned elsewhere, which has no authority to hand
// out and nothing to install.
func (p *TrustPlugin) Manages() bool { return p != nil && p.Manager != nil }

// Installed reports whether the local authority is in this machine's trust
// store.
func (p *TrustPlugin) Installed() bool {
	if p == nil || p.Manager == nil {
		return false
	}
	return p.Manager.CAInstalled()
}

// Fingerprint identifies the authority, for someone checking that the one their
// browser trusts is this one.
func (p *TrustPlugin) Fingerprint() (string, error) {
	if p == nil || p.Manager == nil {
		return "", nil
	}
	return p.Manager.GetCAFingerprint()
}

// Install puts the local authority in this machine's trust store, reissuing the
// certificate under it. The listener serving that certificate binds again on
// its own; see [tlspkg.CertificateWatcher].
//
// The operating system prompts for a password, so this blocks. The menu entry
// calls it off the dispatch goroutine.
func (p *TrustPlugin) Install() error {
	if p == nil || p.Manager == nil {
		return nil
	}
	if err := p.Manager.InstallCA(); err != nil {
		return err
	}
	p.refresh()
	return nil
}

// Regenerate reissues the certificate by whichever route this build already
// uses, without installing an authority that was deliberately never installed.
func (p *TrustPlugin) Regenerate() error {
	if p == nil || p.Manager == nil {
		return nil
	}
	return p.Manager.RegenerateCertificates()
}

// install runs Install from the menu, off the dispatch goroutine: a handler
// waiting on a password prompt would freeze every other menu item.
func (p *TrustPlugin) install() {
	go func() {
		p.logf("Installing a certificate authority so browsers trust this agent; your operating system may ask for a password")

		if err := p.Install(); err != nil {
			p.logf("Could not trust this agent in browsers: %v", err)
			return
		}
		p.logf("Browsers on this machine now trust this agent; reload any page that uses the reader")
	}()
}

// refresh hides the entry once there is nothing left for it to do. Safe from
// any goroutine, as Install needs.
func (p *TrustPlugin) refresh() {
	if p.entry != nil {
		p.entry.SetVisible(!p.Installed())
	}
}

func (p *TrustPlugin) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

func (p *TrustPlugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Trust This Agent in Browsers"
}
