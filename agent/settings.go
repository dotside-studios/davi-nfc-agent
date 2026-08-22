package agent

import (
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

	// Each setter refuses a field the launcher holds, so a stored preference
	// and an operator's click meet the same rule in the same place.
	a.SetReaderMode(settings.ParseMode(s.Mode))
	a.SetCardTypeFilter(s.CardTypes)
	a.SetPinnedDevice(s.DevicePath)
	a.SetRequirePairedDevice(s.RequirePairedDevice)
	a.SetReaderFeedback(s.ReaderFeedback)
	a.setDevicePort(s.Port)

	a.notifySettingsChanged()
}

// SetExplicit records what the launcher set, from flags, the environment, or a
// program embedding the agent. Call it before the agent serves anything: it
// decides which stored preferences are applied at all.
func (a *Agent) SetExplicit(e settings.Explicit) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.explicit = e
}

// Explicit reports the settings the launcher holds for this run.
func (a *Agent) Explicit() settings.Explicit {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.explicit
}

// launcherHolds reports that a field was set at launch, so nothing this run may
// change it. It logs, because a change that disappears without a word is what
// leaves an operator believing something untrue of the running agent.
//
// Callers check whether the value would actually change first, so re-applying
// the same stored file says nothing.
func (a *Agent) launcherHolds(what string, explicit bool) bool {
	if !explicit {
		return false
	}
	a.logger.Printf("Ignoring a change to %s: it was set at launch and holds until the agent is restarted", what)
	return true
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
		Port:                a.DevicePort(),
		RequirePairedDevice: a.RequirePairedDevice(),
		ReaderFeedback:      a.ReaderFeedback(),
	}
}

// SetReaderMode changes the reader's access mode, on a running reader as well
// as on the next one the agent starts. Accepted with no reader running, because
// the mode is the agent's and Start hands it to the reader it builds.
func (a *Agent) SetReaderMode(mode nfc.ReaderMode) {
	if a.CurrentReaderMode() == mode {
		return
	}
	if a.launcherHolds("the reader mode", a.Explicit().Mode) {
		return
	}

	a.settingsMu.Lock()
	a.readerMode = mode
	a.settingsMu.Unlock()

	if reader := a.reader.Load(); reader != nil {
		reader.SetMode(mode)
	}

	a.notifySettingsChanged()
}

// CurrentReaderMode is the mode the reader is in, or the one the next reader
// will start in while there is none.
func (a *Agent) CurrentReaderMode() nfc.ReaderMode {
	if reader := a.reader.Load(); reader != nil {
		return reader.GetMode()
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.readerMode
}

// SetCardTypeFilter replaces the whole filter, which is what an operator
// picking types is doing. An empty list is no filter at all.
func (a *Agent) SetCardTypeFilter(cardTypes []string) {
	next := settings.Normalize(settings.Settings{CardTypes: cardTypes}).CardTypes
	if sameCardTypes(a.CardTypeFilter(), next) {
		return
	}
	if a.launcherHolds("the card-type filter", a.Explicit().CardTypes) {
		return
	}

	a.cardTypes.replace(next)
	a.notifySettingsChanged()
}

// CardTypeFilter lists the allowed card types, sorted as they are stored. Nil
// when nothing is filtered.
func (a *Agent) CardTypeFilter() []string {
	return a.cardTypes.list()
}

// SetPinnedDevice records the reader the operator chose, empty for auto-detect.
// Recording the choice does not start that reader.
func (a *Agent) SetPinnedDevice(devicePath string) {
	if a.CurrentPinnedDevice() == devicePath {
		return
	}
	if a.launcherHolds("the pinned reader", a.Explicit().DevicePath) {
		return
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pinnedDevice = devicePath
}

// CurrentPinnedDevice is the reader the operator chose, empty for auto-detect.
func (a *Agent) CurrentPinnedDevice() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.pinnedDevice
}

// adoptReaderSettings hands a freshly built reader the settings the agent holds.
// Start creates the reader long after the stored settings were applied, so
// without this a read-only agent comes back from every restart able to write.
func (a *Agent) adoptReaderSettings() {
	reader := a.reader.Load()
	if reader == nil {
		return
	}

	a.settingsMu.RLock()
	mode, feedback := a.readerMode, a.readerFeedback
	a.settingsMu.RUnlock()

	reader.SetMode(mode)
	reader.SetFeedback(feedback)
}

// setDevicePort records the port the agent should serve on. The listener keeps
// its current port until it is rebound, so the console asks for a restart after
// saving one.
func (a *Agent) setDevicePort(port int) {
	if port <= 0 || port == a.DevicePort() {
		return
	}
	if a.launcherHolds("the agent port", a.Explicit().Port) {
		return
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.devicePort = port
}

// sameCardTypes compares two normalized filters.
func sameCardTypes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// notifySettingsChanged runs the registered change hooks, so a change made from
// the tray reaches whatever is displaying it. Master calls this notifyConsole
// and reaches a console field; this agent does not know what a console is, so
// the observers registered through OnClientsChange are what it tells.
func (a *Agent) notifySettingsChanged() {
	if fn := a.clientsChanged(); fn != nil {
		fn()
	}
}
