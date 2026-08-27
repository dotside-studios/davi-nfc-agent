// Package secure restricts a path to the current user, for the files an
// application persists that nothing else should read: certificates, private
// keys, and the credentials an agent issues.
//
// It is a package of its own because more than one thing has such a file, and
// how a path is restricted is per-platform rather than per-caller.
package secure

// Dir applies restrictive permissions to a directory:
//   - Unix: mode 0700 (current user only)
//   - Windows: a protected DACL granting full access only to the current user,
//     BUILTIN\Administrators, and SYSTEM
func Dir(path string) error { return secureDir(path) }

// File applies restrictive permissions to a file:
//   - Unix: mode 0600 (current user only)
//   - Windows: a protected DACL, the same shape as [Dir]
func File(path string) error { return secureFile(path) }
