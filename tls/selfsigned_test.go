package tls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	m := NewManager(t.TempDir())
	if err := os.MkdirAll(m.tlsDir, 0700); err != nil {
		t.Fatalf("create tls dir: %v", err)
	}
	return m
}

func TestSelfSignedCertificateLoads(t *testing.T) {
	m := newTestManager(t)

	if err := m.generateSelfSigned([]string{"localhost", "127.0.0.1", "kiosk.local"}); err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}

	pair, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	if err != nil {
		t.Fatalf("the generated pair does not load: %v", err)
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("certificate is not valid for localhost: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("certificate is not valid for 127.0.0.1: %v", err)
	}
	if err := leaf.VerifyHostname("kiosk.local"); err != nil {
		t.Errorf("certificate is not valid for kiosk.local: %v", err)
	}
}

// The pin has to survive reissuance, or a device would be locked out every time
// the host changed network. This is the property the whole design rests on.
func TestPublicKeyPinSurvivesReissue(t *testing.T) {
	m := newTestManager(t)

	if err := m.generateSelfSigned([]string{"localhost", "127.0.0.1"}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	pin, err := m.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin: %v", err)
	}
	firstCert, err := os.ReadFile(m.certFile)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}

	// Reissue for a different set of addresses, as a network change would.
	if err := m.generateSelfSigned([]string{"localhost", "127.0.0.1", "192.168.1.50"}); err != nil {
		t.Fatalf("reissue: %v", err)
	}

	secondPin, err := m.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin after reissue: %v", err)
	}
	secondCert, err := os.ReadFile(m.certFile)
	if err != nil {
		t.Fatalf("read reissued cert: %v", err)
	}

	if string(firstCert) == string(secondCert) {
		t.Fatal("the certificate did not actually change; the test proves nothing")
	}
	if pin != secondPin {
		t.Errorf("pin changed across reissue: %s -> %s", pin, secondPin)
	}
}

// A restart must not invalidate a pin either.
func TestPublicKeyPinSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := NewManager(dir)
	if err := os.MkdirAll(first.tlsDir, 0700); err != nil {
		t.Fatalf("create tls dir: %v", err)
	}
	pin, err := first.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin: %v", err)
	}

	second := NewManager(dir)
	reloaded, err := second.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin after restart: %v", err)
	}

	if pin != reloaded {
		t.Errorf("pin changed across restart: %s -> %s", pin, reloaded)
	}
}

func TestPublicKeyPinFormat(t *testing.T) {
	m := newTestManager(t)

	pin, err := m.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin: %v", err)
	}

	// Same shape as an HPKP value, so it is recognizable and comparable.
	if len(pin) < len("sha256/") || pin[:len("sha256/")] != "sha256/" {
		t.Errorf("pin = %q, want a sha256/ prefix", pin)
	}
	// base64 of a 32-byte digest is 44 characters.
	if got := len(pin) - len("sha256/"); got != 44 {
		t.Errorf("digest length = %d, want 44", got)
	}
}

// The private key must never be world-readable.
func TestLeafKeyPermissions(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Unix mode bits do not apply")
	}

	m := newTestManager(t)
	if _, err := m.loadOrCreateLeafKey(); err != nil {
		t.Fatalf("loadOrCreateLeafKey: %v", err)
	}

	info, err := os.Stat(m.leafKeyPath())
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("key mode = %o, want no group or world access", mode)
	}
}

// Without an opt-in, no certificate authority is created at all.
func TestSelfSignedIsTheDefault(t *testing.T) {
	m := newTestManager(t)

	if m.CAInstalled() {
		t.Fatal("a fresh install already reports a CA")
	}
	if err := m.generate([]string{"localhost", "127.0.0.1"}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	if _, err := os.Stat(m.caCertFile); !os.IsNotExist(err) {
		t.Error("a certificate authority was created without being asked for")
	}

	data, err := os.ReadFile(m.certFile)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("certificate is not valid PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if leaf.Issuer.CommonName != leaf.Subject.CommonName {
		t.Errorf("certificate is not self-signed: issuer %q, subject %q",
			leaf.Issuer.CommonName, leaf.Subject.CommonName)
	}
}
