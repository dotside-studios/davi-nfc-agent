package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
)

// secretFileName is where the auto-generated API secret lives under
// the agent's config directory. The file is mode 0600 on Unix and
// has the same DACL hardening as the TLS dir on Windows.
const secretFileName = "api-secret.txt"

// loadOrCreateAPISecret reads the persisted API secret from configDir,
// generating and persisting a fresh one if absent.
//
// Returns the secret string and a boolean indicating whether the
// secret was newly generated (so callers can log it).
func loadOrCreateAPISecret(configDir string) (string, bool, error) {
	if configDir == "" {
		return "", false, fmt.Errorf("config dir is empty")
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", false, fmt.Errorf("create config dir: %w", err)
	}
	// Apply Windows DACL / Unix chmod via the same helper used for TLS.
	_ = tlspkg.SecureDir(configDir)

	path := filepath.Join(configDir, secretFileName)
	if data, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(data))
		if secret != "" {
			return secret, false, nil
		}
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("read secret file: %w", err)
	}

	secret, err := generateAPISecret()
	if err != nil {
		return "", false, fmt.Errorf("generate secret: %w", err)
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		return "", false, fmt.Errorf("write secret file: %w", err)
	}
	_ = tlspkg.SecureFile(path)
	return secret, true, nil
}

// rotateAPISecret writes a fresh secret to the config dir, replacing
// any existing one.
func rotateAPISecret(configDir string) (string, error) {
	if configDir == "" {
		return "", fmt.Errorf("config dir is empty")
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	_ = tlspkg.SecureDir(configDir)

	secret, err := generateAPISecret()
	if err != nil {
		return "", err
	}
	path := filepath.Join(configDir, secretFileName)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		return "", fmt.Errorf("write secret file: %w", err)
	}
	_ = tlspkg.SecureFile(path)
	return secret, nil
}

// generateAPISecret produces a 32-byte URL-safe base64 token.
func generateAPISecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
