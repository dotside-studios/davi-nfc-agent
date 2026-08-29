package tls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	if mgr.configDir != tmpDir {
		t.Errorf("configDir = %q, want %q", mgr.configDir, tmpDir)
	}

	expectedTLSDir := filepath.Join(tmpDir, "tls")
	if mgr.tlsDir != expectedTLSDir {
		t.Errorf("tlsDir = %q, want %q", mgr.tlsDir, expectedTLSDir)
	}

	expectedCertFile := filepath.Join(expectedTLSDir, "server.crt")
	if mgr.certFile != expectedCertFile {
		t.Errorf("certFile = %q, want %q", mgr.certFile, expectedCertFile)
	}

	expectedKeyFile := filepath.Join(expectedTLSDir, "server.key")
	if mgr.keyFile != expectedKeyFile {
		t.Errorf("keyFile = %q, want %q", mgr.keyFile, expectedKeyFile)
	}
}

func TestHostsChanged(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	// Create TLS directory
	if err := os.MkdirAll(mgr.tlsDir, 0700); err != nil {
		t.Fatalf("failed to create TLS dir: %v", err)
	}

	// No cached hosts - should report changed
	if !mgr.hostsChanged([]string{"localhost"}) {
		t.Error("Expected hostsChanged=true when no cached hosts exist")
	}

	// Write some hosts
	err := mgr.writeCachedHosts([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("writeCachedHosts failed: %v", err)
	}

	// Same hosts - should not be changed
	if mgr.hostsChanged([]string{"localhost", "127.0.0.1"}) {
		t.Error("Expected hostsChanged=false for same hosts")
	}

	// Same hosts different order - should not be changed
	if mgr.hostsChanged([]string{"127.0.0.1", "localhost"}) {
		t.Error("Expected hostsChanged=false for same hosts in different order")
	}

	// Different hosts - should be changed
	if !mgr.hostsChanged([]string{"localhost", "127.0.0.1", "192.168.1.1"}) {
		t.Error("Expected hostsChanged=true for different hosts")
	}

	// Fewer hosts - should be changed
	if !mgr.hostsChanged([]string{"localhost"}) {
		t.Error("Expected hostsChanged=true for fewer hosts")
	}
}

func TestReadWriteCachedHosts(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	// Create TLS directory
	if err := os.MkdirAll(mgr.tlsDir, 0700); err != nil {
		t.Fatalf("failed to create TLS dir: %v", err)
	}

	hosts := []string{"localhost", "127.0.0.1", "192.168.1.100"}

	err := mgr.writeCachedHosts(hosts)
	if err != nil {
		t.Fatalf("writeCachedHosts failed: %v", err)
	}

	readHosts, err := mgr.readCachedHosts()
	if err != nil {
		t.Fatalf("readCachedHosts failed: %v", err)
	}

	if len(readHosts) != len(hosts) {
		t.Fatalf("readHosts length = %d, want %d", len(readHosts), len(hosts))
	}

	for i, h := range hosts {
		if readHosts[i] != h {
			t.Errorf("readHosts[%d] = %q, want %q", i, readHosts[i], h)
		}
	}
}

func TestCertsExist(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	// Create TLS directory
	if err := os.MkdirAll(mgr.tlsDir, 0700); err != nil {
		t.Fatalf("failed to create TLS dir: %v", err)
	}

	// No certs - should not exist
	if mgr.certsExist() {
		t.Error("Expected certsExist=false when no certs")
	}

	// Create only cert file
	if err := os.WriteFile(mgr.certFile, []byte("cert"), 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	if mgr.certsExist() {
		t.Error("Expected certsExist=false when only cert exists")
	}

	// Create key file too
	if err := os.WriteFile(mgr.keyFile, []byte("key"), 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	if !mgr.certsExist() {
		t.Error("Expected certsExist=true when both files exist")
	}
}

func TestSameHosts(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"identical order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"reordered", []string{"a", "b", "c"}, []string{"c", "a", "b"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"ipv6 + ipv4 mix", []string{"127.0.0.1", "::1", "192.168.1.5"}, []string{"::1", "192.168.1.5", "127.0.0.1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origA := append([]string(nil), tt.a...)
			origB := append([]string(nil), tt.b...)
			if got := sameHosts(tt.a, tt.b); got != tt.want {
				t.Errorf("sameHosts(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			// Inputs must not be mutated.
			if !reflect.DeepEqual(tt.a, origA) || !reflect.DeepEqual(tt.b, origB) {
				t.Errorf("sameHosts mutated its inputs: a=%v (was %v), b=%v (was %v)", tt.a, origA, tt.b, origB)
			}
		})
	}
}

// TestWatchReissuesConcurrency exercises the watcher init/teardown paths
// under concurrent callers. Run with -race to detect data races on the
// networkChangeChan / stopWatchChan / lastHosts fields.
func TestWatchReissuesConcurrency(t *testing.T) {
	mgr := NewManager(t.TempDir())

	const goroutines = 20
	var wg sync.WaitGroup

	// Concurrent WatchReissues callers must all return the same channel.
	chans := make([]<-chan struct{}, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			chans[idx] = mgr.WatchReissues()
		}(i)
	}
	wg.Wait()

	first := chans[0]
	if first == nil {
		t.Fatal("WatchReissues returned nil channel")
	}
	for i, ch := range chans {
		if ch != first {
			t.Errorf("call %d returned a different channel; expected single shared channel", i)
		}
	}

	// Concurrent StopWatching calls must not panic on double-close.
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			mgr.StopWatching()
		}()
	}
	wg.Wait()
}

// Reissuing a certificate must not be a back door into the system trust store.
// generateCertificates installs a CA; generate only reaches it when this
// install already uses one, and RegenerateCertificates has to honour that.
func TestRegenerateKeepsASelfSignedInstallSelfSigned(t *testing.T) {
	m := NewManager(t.TempDir())

	if m.usesCA() {
		t.Fatal("a fresh manager takes the CA route; the default is self-signed")
	}

	if err := m.RegenerateCertificates(); err != nil {
		t.Fatalf("RegenerateCertificates: %v", err)
	}

	if m.CAInstalled() {
		t.Error("reissuing a certificate created a certificate authority")
	}
	if m.usesCA() {
		t.Error("reissuing a certificate switched the install to the CA route")
	}

	// The issued certificate is its own issuer, i.e. nothing signed it.
	pemBytes, err := os.ReadFile(m.GetCertFile())
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("certificate file holds no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Issuer.String() != cert.Subject.String() {
		t.Errorf("certificate was issued by %q, want a self-signed one", cert.Issuer)
	}
}

func TestUseCASelectsTheCARoute(t *testing.T) {
	m := NewManager(t.TempDir())
	if m.usesCA() {
		t.Fatal("self-signed is the default")
	}
	m.UseCA(true)
	if !m.usesCA() {
		t.Error("UseCA(true) did not select the CA route")
	}
}
