package tls

import (
	"os"
	"path/filepath"
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
	os.MkdirAll(mgr.tlsDir, 0700)

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
	os.MkdirAll(mgr.tlsDir, 0700)

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
	os.MkdirAll(mgr.tlsDir, 0700)

	// No certs - should not exist
	if mgr.certsExist() {
		t.Error("Expected certsExist=false when no certs")
	}

	// Create only cert file
	os.WriteFile(mgr.certFile, []byte("cert"), 0600)
	if mgr.certsExist() {
		t.Error("Expected certsExist=false when only cert exists")
	}

	// Create key file too
	os.WriteFile(mgr.keyFile, []byte("key"), 0600)
	if !mgr.certsExist() {
		t.Error("Expected certsExist=true when both files exist")
	}
}

// TestWatchNetworkChangesConcurrency exercises the watcher init/teardown paths
// under concurrent callers. Run with -race to detect data races on the
// networkChangeChan / stopWatchChan / lastHosts fields.
func TestWatchNetworkChangesConcurrency(t *testing.T) {
	mgr := NewManager(t.TempDir())

	const goroutines = 20
	var wg sync.WaitGroup

	// Concurrent WatchNetworkChanges callers must all return the same channel.
	chans := make([]<-chan struct{}, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			chans[idx] = mgr.WatchNetworkChanges()
		}(i)
	}
	wg.Wait()

	first := chans[0]
	if first == nil {
		t.Fatal("WatchNetworkChanges returned nil channel")
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
