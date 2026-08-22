package main

import (
	"sort"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// The agent holds the settings; the file only persists them. ApplySettings puts
// a stored file in force and Settings reports what is in force, so the console
// and the tray can display the agent's state instead of the last save.

// ApplySettings puts stored settings in force.
//
// Every field lands on the agent even when the component it configures does not
// exist yet, because the reader is built in Start, after this runs.
//
// DevicePath pins the reader without switching to one: selecting a reader
// restarts it, which is the explicit action's job, not a side effect of
// applying preferences.
func (a *Agent) ApplySettings(s settings.Settings) {
	if a == nil {
		return
	}

	a.SetReaderMode(settings.ParseMode(s.Mode))

	a.ClearCardTypeFilter()
	for _, cardType := range s.CardTypes {
		a.AllowCardType(cardType)
	}

	a.SetPinnedDevice(s.DevicePath)
	a.SetRequirePairedDevice(s.RequirePairedDevice)
	a.SetReaderFeedback(s.ReaderFeedback)
	a.setDevicePort(s.Port)

	a.notifyConsole()
}

// Settings reports the agent's live configuration in the shape it is stored in.
// The answer comes from the agent, so a mode switched from the tray and a
// requirement the command line will not let a preference withdraw both show up
// as what they actually are.
func (a *Agent) Settings() settings.Settings {
	if a == nil {
		return settings.Defaults()
	}

	return settings.Settings{
		Mode:                settings.FormatMode(a.CurrentReaderMode()),
		CardTypes:           a.CardTypeFilter(),
		DevicePath:          a.CurrentPinnedDevice(),
		Port:                a.ConfiguredPort(),
		RequirePairedDevice: a.RequiresPairedDevice(),
		ReaderFeedback:      a.ReaderFeedbackEnabled(),
	}
}

// SetPinnedDevice records the reader the operator chose, empty for auto-detect.
// Recording the choice does not start that reader.
func (a *Agent) SetPinnedDevice(devicePath string) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.PinnedDevice = devicePath
}

// CurrentPinnedDevice is the reader the operator chose, empty for auto-detect.
func (a *Agent) CurrentPinnedDevice() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.PinnedDevice
}

// adoptReaderSettings hands a freshly built reader the settings the agent holds.
// Start creates the reader long after the stored settings were applied, so
// without this a read-only agent comes back from every restart able to write.
func (a *Agent) adoptReaderSettings() {
	if a.Reader == nil {
		return
	}

	a.settingsMu.RLock()
	mode, feedback := a.ReaderMode, a.ReaderFeedback
	a.settingsMu.RUnlock()

	a.Reader.SetMode(mode)
	a.Reader.SetFeedback(feedback)
}

// CurrentReaderMode is the mode the reader is in, or the one the next reader
// will start in while there is none.
func (a *Agent) CurrentReaderMode() nfc.ReaderMode {
	if a.Reader != nil {
		return a.Reader.GetMode()
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.ReaderMode
}

// ReaderFeedbackEnabled reports whether the reader announces what it does,
// asking the reader itself when there is one.
func (a *Agent) ReaderFeedbackEnabled() bool {
	if a.Reader != nil {
		return a.Reader.FeedbackEnabled()
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.ReaderFeedback
}

// CardTypeFilter lists the allowed card types, sorted as they are stored. Nil
// when nothing is filtered.
func (a *Agent) CardTypeFilter() []string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()

	if len(a.AllowedCardTypes) == 0 {
		return nil
	}

	out := make([]string, 0, len(a.AllowedCardTypes))
	for cardType := range a.AllowedCardTypes {
		out = append(out, cardType)
	}
	sort.Strings(out)
	return out
}

// setDevicePort records the port the agent should serve on. The listener keeps
// its current port until it is rebound, so the console asks for a restart after
// saving one.
func (a *Agent) setDevicePort(port int) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()

	if port <= 0 || port == a.DevicePort {
		return
	}
	if a.DevicePortLocked {
		// A stored preference may not move a listener the command line placed,
		// as with the pairing requirement.
		a.Logger.Printf("Ignoring stored port %d: the port was set on the command line", port)
		return
	}
	a.DevicePort = port
}

// notifyConsole redraws the console, so a change made from the tray reaches it.
func (a *Agent) notifyConsole() {
	if a.Console != nil {
		a.Console.NotifyChange()
	}
}
