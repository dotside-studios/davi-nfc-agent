package agent

import (
	"os"
	"path/filepath"
	"testing"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// The entry offers the one thing it does, and stops offering it once that is
// done. Installing an authority is not a decision taken once: CAInstalled reads
// the filesystem every time, so a config directory that loses its authority
// needs the offer back without an agent restart to notice.
func TestTrustEntryFollowsTheCertificateAuthority(t *testing.T) {
	dir := t.TempDir()
	trust := &TrustPlugin{Manager: tlspkg.NewManager(dir)}

	a := quietAgent(t, trust)
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	entry := fake.Find("Trust This Agent in Browsers")
	if entry == nil {
		t.Fatalf("the trust entry is missing:\n%s", fake.Render())
	}
	if !entry.Visible() {
		t.Fatal("the trust entry is hidden with no certificate authority installed")
	}

	caFile := filepath.Join(dir, "ca", "rootCA.pem")
	if err := os.MkdirAll(filepath.Dir(caFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, []byte("ca"), 0600); err != nil {
		t.Fatal(err)
	}
	a.notifyServerRestart()
	if entry.Visible() {
		t.Fatal("the trust entry is still offered with a certificate authority installed")
	}

	if err := os.Remove(caFile); err != nil {
		t.Fatal(err)
	}
	a.notifyServerRestart()
	if !entry.Visible() {
		t.Fatal("the trust entry stayed hidden after the certificate authority went missing")
	}
}

// A build that manages no certificate holds a plugin with no manager. Nothing
// it is asked reaches for one, and the entry it would have added is not there
// to be clicked.
func TestTrustPluginWithoutAManagerIsInert(t *testing.T) {
	trust := &TrustPlugin{}

	a := quietAgent(t, trust)
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if entry := fake.Find("Trust This Agent in Browsers"); entry != nil {
		t.Errorf("the trust entry is offered by a build managing no certificate:\n%s", fake.Render())
	}
	if trust.Manages() {
		t.Error("Manages reported true with no manager")
	}
	if trust.Installed() {
		t.Error("Installed reported true with no manager")
	}
	if err := trust.Install(); err != nil {
		t.Errorf("Install: %v", err)
	}
	if err := trust.Regenerate(); err != nil {
		t.Errorf("Regenerate: %v", err)
	}
}
