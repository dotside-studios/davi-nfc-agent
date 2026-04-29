//go:build !windows

package tls

import "os"

// secureDir restricts a directory to the current user (mode 0700).
// On Unix this is a no-op beyond os.Chmod since the mode is already supplied
// to MkdirAll, but calling Chmod ensures correctness even when the directory
// already existed with looser permissions.
func secureDir(path string) error {
	return os.Chmod(path, 0700)
}

// secureFile restricts a file to the current user (mode 0600).
func secureFile(path string) error {
	return os.Chmod(path, 0600)
}
