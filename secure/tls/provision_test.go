package tls

import (
	"os"
	"testing"
)

// Provision is what a program calls in place of the certificate block Setup
// used to run, so what it reports has to be enough to serve and present the
// certificate without reaching for the manager.
func TestProvisionReportsWhatAListenerNeeds(t *testing.T) {
	got, err := Provision(t.TempDir(), false)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if got.Manager == nil {
		t.Fatal("Provision reported no manager, so nothing can rebind on a reissue")
	}
	if got.CertFile != got.Manager.GetCertFile() || got.KeyFile != got.Manager.GetKeyFile() {
		t.Errorf("CertFile/KeyFile = %q/%q, want the manager's %q/%q",
			got.CertFile, got.KeyFile, got.Manager.GetCertFile(), got.Manager.GetKeyFile())
	}
	for _, path := range []string{got.CertFile, got.KeyFile} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Provision reported %q, which is not on disk: %v", path, err)
		}
	}
	if got.PublicKeyPin == "" {
		t.Error("Provision reported no key pin, so a device has nothing to recognise the agent by")
	}
}

// A restart must not hand devices a different agent: the pin is what they
// recognise it by, and reissuing on every start would lock every paired device
// out.
func TestProvisionReusesTheCertificate(t *testing.T) {
	dir := t.TempDir()

	first, err := Provision(dir, false)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	second, err := Provision(dir, false)
	if err != nil {
		t.Fatalf("Provision again: %v", err)
	}

	if second.PublicKeyPin != first.PublicKeyPin {
		t.Errorf("the pin changed across a restart: %q then %q", first.PublicKeyPin, second.PublicKeyPin)
	}
	if second.CertFile != first.CertFile {
		t.Errorf("CertFile changed across a restart: %q then %q", first.CertFile, second.CertFile)
	}
}

// A directory it cannot write is reported rather than returning a half-built
// certificate the caller would serve.
func TestProvisionReportsAFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := dir + "/blocked"
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := Provision(blocked, false)
	if err == nil {
		t.Fatal("Provision reported no error for a path it cannot use")
	}
	if got.Manager != nil || got.CertFile != "" || got.PublicKeyPin != "" {
		t.Errorf("a failed Provision reported %+v, want the zero value", got)
	}
}
