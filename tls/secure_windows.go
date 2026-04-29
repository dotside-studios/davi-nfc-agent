//go:build windows

package tls

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// secureDir restricts a directory's DACL to the current user, SYSTEM, and
// BUILTIN\Administrators (full control each). Inheritance from the parent is
// disabled so loosely permissioned ancestor directories cannot leak access.
func secureDir(path string) error { return restrictACL(path) }

// secureFile restricts a file's DACL to the current user, SYSTEM, and
// BUILTIN\Administrators (full control each).
func secureFile(path string) error { return restrictACL(path) }

func restrictACL(path string) error {
	token := windows.GetCurrentProcessToken()
	tu, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get current user: %w", err)
	}
	userSID := tu.User.Sid.String()

	// SDDL: protected (P), auto-inherited disabled. Three explicit ACEs granting
	// File-All to the user, BUILTIN\Administrators (BA), and LocalSystem (SY).
	// OICI = inherit by both contained objects and child containers (no-op on files).
	sddl := fmt.Sprintf(
		"D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)",
		userSID,
	)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("parse SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}
