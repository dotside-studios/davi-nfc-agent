//go:build hardware

package hwtest

import (
	"path/filepath"
	"testing"
)

// The helpers the hardware tests lean on, which need no reader.

func TestChooseReader(t *testing.T) {
	names := []string{"Generic Reader 00 00", "ACS ACR122U PICC Interface 00 00", "ACS ACR1252 1S CL Reader"}
	for _, tt := range []struct {
		want, got string
		fails     bool
	}{
		{"", names[0], false},
		{"acr122", names[1], false},
		{"ACR1252", names[2], false},
		{"nonesuch", "", true},
	} {
		got, err := chooseReader(names, tt.want)
		if (err != nil) != tt.fails || got != tt.got {
			t.Errorf("chooseReader(%q) = %q, %v, want %q (fails %v)", tt.want, got, err, tt.got, tt.fails)
		}
	}
}

func TestFileSafe(t *testing.T) {
	if got := fileSafe("ACS ACR122U PICC Interface 00 00"); got != "acs-acr122u-picc-interface-00-00" {
		t.Errorf("fileSafe = %q", got)
	}
}

func TestOutDir(t *testing.T) {
	root := repoRoot(t)
	t.Setenv(envOut, "")
	if got := outDir(t); got != filepath.Join(root, "hwtest-out") {
		t.Errorf("default out dir = %q", got)
	}
	t.Setenv(envOut, "captures")
	if got := outDir(t); got != filepath.Join(root, "captures") {
		t.Errorf("relative out dir = %q", got)
	}
	abs := t.TempDir()
	t.Setenv(envOut, abs)
	if got := outDir(t); got != abs {
		t.Errorf("absolute out dir = %q", got)
	}
}
