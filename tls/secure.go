package tls

// SecureDir applies restrictive permissions to a directory:
//   - Unix: mode 0700 (current user only)
//   - Windows: a protected DACL granting full access only to the
//     current user, BUILTIN\Administrators, and SYSTEM
//
// Exported wrapper over the per-OS secureDir so callers outside the
// tls package (e.g. the agent's config dir) can apply the same
// hardening.
func SecureDir(path string) error { return secureDir(path) }

// SecureFile applies restrictive permissions to a file:
//   - Unix: mode 0600 (current user only)
//   - Windows: a protected DACL (same shape as SecureDir)
func SecureFile(path string) error { return secureFile(path) }
