package console

import "github.com/dotside-studios/davi-nfc-agent/settings"

// Tray is the console's view of the system tray: the actions the console can
// take that must also move the tray's menu, so the two never disagree about
// what the agent is doing.
//
// It is declared here, not in agent/tray, so the dependency runs one way —
// the tray imports the console to open it, and satisfies this in return.
type Tray interface {
	// StopAgent stops the reader and updates the menu to match.
	StopAgent()
	// Quit shuts the whole agent down.
	Quit()
	// SwitchDevice moves the tray's device selection.
	SwitchDevice(devicePath string)
	// RefreshTrustMenu redraws the certificate-trust entries.
	RefreshTrustMenu()
	// SyncSettingsToMenu reflects a settings change made in the console.
	SyncSettingsToMenu(next settings.Settings)
}
