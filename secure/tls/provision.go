package tls

// Provisioned is a certificate this agent manages for itself, and what a build
// needs to serve and present it.
//
// The zero value is a build serving no certificate of its own, which is what a
// caller gets when provisioning was not asked for.
type Provisioned struct {
	// Manager reissues the certificate and holds the authority behind it. Nil
	// when there is none: give it to a listener to rebind on a reissue, to the
	// plugin that installs the authority, and to the one that hands it to a
	// pairing device.
	Manager *Manager

	// CertFile and KeyFile are the pair a listener serves. Blank for a build
	// serving plain HTTP.
	CertFile string
	KeyFile  string

	// PublicKeyPin identifies the agent to devices across reissues, so they
	// need no trust store to recognise it. Blank when there is no certificate.
	PublicKeyPin string
}

// Provision manages a certificate under dir, creating one on first run and
// reusing it afterwards. installCA also puts the authority behind it in this
// machine's trust store, so browsers accept the agent.
//
// It is the program's call rather than the agent's: what serves a certificate,
// hands out its authority and offers to install it is decided by whoever
// assembles the build. A caller serving a certificate provisioned elsewhere
// does not call this at all and names the pair directly.
func Provision(dir string, installCA bool) (Provisioned, error) {
	m := NewManager(dir)
	m.UseCA(installCA)

	if _, _, err := m.EnsureCertificates(); err != nil {
		return Provisioned{}, err
	}

	out := Provisioned{Manager: m, CertFile: m.GetCertFile(), KeyFile: m.GetKeyFile()}
	// A pin the caller cannot read is not fatal: the certificate still serves,
	// and only a device pinning the agent is affected.
	if pin, err := m.PublicKeyPin(); err == nil {
		out.PublicKeyPin = pin
	}
	return out, nil
}
