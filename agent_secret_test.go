package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateAPISecret_FirstRun(t *testing.T) {
	dir := t.TempDir()
	secret, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !fresh {
		t.Errorf("first run should report fresh=true")
	}
	if len(secret) < 32 {
		t.Errorf("secret too short: %d chars", len(secret))
	}

	// File must exist with the secret on disk.
	data, err := os.ReadFile(filepath.Join(dir, secretFileName))
	if err != nil {
		t.Fatalf("read persisted secret: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != secret {
		t.Errorf("on-disk secret %q != returned %q", got, secret)
	}
}

func TestLoadOrCreateAPISecret_Persists(t *testing.T) {
	dir := t.TempDir()

	first, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	second, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if fresh {
		t.Errorf("second run should report fresh=false")
	}
	if first != second {
		t.Errorf("secret changed across calls: %q vs %q", first, second)
	}
}

func TestLoadOrCreateAPISecret_RegenerateOnEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, secretFileName)

	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	secret, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !fresh {
		t.Errorf("empty file should trigger fresh=true")
	}
	if len(secret) < 32 {
		t.Errorf("secret too short")
	}
}

func TestRotateAPISecret(t *testing.T) {
	dir := t.TempDir()
	first, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rotated, err := rotateAPISecret(dir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == first {
		t.Errorf("rotateAPISecret returned the same value")
	}

	// Subsequent load should return the rotated value.
	loaded, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("post-rotate load: %v", err)
	}
	if loaded != rotated {
		t.Errorf("loaded %q != rotated %q", loaded, rotated)
	}
}

func TestGenerateAPISecret(t *testing.T) {
	seen := make(map[string]struct{})
	for range 50 {
		s, err := generateAPISecret()
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		if len(s) < 32 {
			t.Errorf("too short: %d", len(s))
		}
		if _, dup := seen[s]; dup {
			t.Errorf("collision: %q", s)
		}
		seen[s] = struct{}{}
	}
}
