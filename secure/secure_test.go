//go:build !windows

package secure

import (
	"os"
	"path/filepath"
	"testing"
)

// The agent's config directory holds device credentials and a private key, so
// nothing but the owner may read it.
func TestDirAndFileAreOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Dir(dir); err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0700 {
		t.Errorf("directory mode = %o, want 700", got)
	}

	path := filepath.Join(dir, "devices.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := File(path); err != nil {
		t.Fatalf("File: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

// A path that is not there is reported rather than silently leaving something
// readable.
func TestMissingPathIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if err := Dir(missing); err == nil {
		t.Error("Dir reported no error for a directory that is not there")
	}
	if err := File(missing); err == nil {
		t.Error("File reported no error for a file that is not there")
	}
}
