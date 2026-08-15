package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
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

// writeTestCA puts a certificate authority where loadCA expects one, in the
// same shape truststore writes: a self-signed root and a PKCS#8 key.
func writeTestCA(t *testing.T, m *Manager) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode CA key: %v", err)
	}

	if err := os.MkdirAll(m.caDir, 0700); err != nil {
		t.Fatalf("create CA dir: %v", err)
	}
	writePEM(t, m.caCertFile, "CERTIFICATE", der)
	writePEM(t, m.caKeyFile, "PRIVATE KEY", keyDER)
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()

	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A CA-signed certificate must carry the same key a self-signed one would.
// When it does not, PublicKeyPin describes a key the handshake never presents,
// so every device that pairs records a pin it can never match.
func TestCASignedCertificateCarriesThePinnedKey(t *testing.T) {
	m := newTestManager(t)
	writeTestCA(t, m)

	caCert, caKey, err := m.loadCA()
	if err != nil {
		t.Fatalf("loadCA: %v", err)
	}
	if err := m.issueLeaf([]string{"localhost", "127.0.0.1"}, caCert, caKey); err != nil {
		t.Fatalf("issueLeaf: %v", err)
	}

	if !m.servedCertMatchesPin() {
		t.Error("the certificate does not carry the key PublicKeyPin reports")
	}

	pair, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	if err != nil {
		t.Fatalf("the generated pair does not load: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	// It is still a CA-issued certificate, which is the point of the route.
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "localhost", Roots: roots}); err != nil {
		t.Errorf("certificate does not chain to the CA: %v", err)
	}
}

// The pin survives switching an install from self-signed to a CA, which is what
// the console's trust action does to an agent with devices already paired.
func TestPinSurvivesAdoptingACA(t *testing.T) {
	m := newTestManager(t)

	if err := m.generateSelfSigned([]string{"localhost"}); err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	before, err := m.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin: %v", err)
	}

	writeTestCA(t, m)
	caCert, caKey, err := m.loadCA()
	if err != nil {
		t.Fatalf("loadCA: %v", err)
	}
	if err := m.issueLeaf([]string{"localhost"}, caCert, caKey); err != nil {
		t.Fatalf("issueLeaf: %v", err)
	}

	after, err := m.PublicKeyPin()
	if err != nil {
		t.Fatalf("PublicKeyPin after adopting a CA: %v", err)
	}
	if before != after {
		t.Errorf("pin changed: %s -> %s", before, after)
	}
	if !m.servedCertMatchesPin() {
		t.Error("the reissued certificate does not carry the pinned key")
	}
}

// An install issued before the CA route signed the persistent key is already
// serving the wrong one. Startup has to notice and reissue, because the pin it
// hands out at pairing is otherwise permanently unsatisfiable.
func TestStartupReissuesWhenTheServedKeyIsNotThePinnedOne(t *testing.T) {
	m := newTestManager(t)

	hosts, err := GetAllHosts()
	if err != nil {
		t.Skipf("GetAllHosts: %v", err)
	}
	if err := m.generateSelfSigned(hosts); err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	if err := m.writeCachedHosts(hosts); err != nil {
		t.Fatalf("writeCachedHosts: %v", err)
	}

	// Stand in for what truststore's MakeCert used to leave behind: a valid
	// pair for the right hosts, over a key nothing has pinned.
	foreign := NewManager(t.TempDir())
	if err := os.MkdirAll(foreign.tlsDir, 0700); err != nil {
		t.Fatalf("create foreign tls dir: %v", err)
	}
	if err := foreign.generateSelfSigned(hosts); err != nil {
		t.Fatalf("generate foreign pair: %v", err)
	}
	for _, f := range [][2]string{{foreign.certFile, m.certFile}, {foreign.keyFile, m.keyFile}} {
		data, err := os.ReadFile(f[0])
		if err != nil {
			t.Fatalf("read %s: %v", f[0], err)
		}
		if err := os.WriteFile(f[1], data, 0600); err != nil {
			t.Fatalf("write %s: %v", f[1], err)
		}
	}

	if m.servedCertMatchesPin() {
		t.Fatal("the foreign pair was not installed")
	}

	if _, _, err := m.EnsureCertificates(); err != nil {
		t.Fatalf("EnsureCertificates: %v", err)
	}
	if !m.servedCertMatchesPin() {
		t.Error("startup left a certificate that does not carry the pinned key")
	}
}
