package agent

import (
	"encoding/json"
	"sort"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Preferences are what an operator can change while the agent runs, and the
// console's only source for one, so it cannot show something the agent is not
// doing.
//
// The agent holds them and nothing here writes them anywhere: a program that
// wants one to outlive the process reads it back and persists it however it
// likes.
type Preferences struct {
	// Mode is the reader access mode.
	Mode nfc.ReaderMode

	// CardTypes is the card-type allowlist. Empty allows every type, including
	// one this build has never heard of.
	CardTypes []string

	// DevicePath pins a reader. Empty is auto-detect.
	DevicePath string

	// Port is the port the agent is set to serve on. A listener keeps the one
	// it is bound on until it is restarted.
	Port int

	RequirePairedDevice bool
	ReaderFeedback      bool
}

// preferencesJSON is the wire shape. The mode travels as its name, so a client
// reads and writes "readwrite" rather than this package's constant, and the
// name is the only representation on the wire: a second field holding the same
// value is one that can disagree with it.
type preferencesJSON struct {
	Mode                string   `json:"mode"`
	CardTypes           []string `json:"cardTypes"`
	DevicePath          string   `json:"devicePath"`
	Port                int      `json:"port"`
	RequirePairedDevice bool     `json:"requirePairedDevice"`
	ReaderFeedback      bool     `json:"readerFeedback"`
}

func (p Preferences) MarshalJSON() ([]byte, error) {
	return json.Marshal(preferencesJSON{
		Mode:                p.Mode.String(),
		CardTypes:           p.CardTypes,
		DevicePath:          p.DevicePath,
		Port:                p.Port,
		RequirePairedDevice: p.RequirePairedDevice,
		ReaderFeedback:      p.ReaderFeedback,
	})
}

// UnmarshalJSON reads the wire shape. An unrecognised mode name reads as
// read/write, which is [nfc.ParseReaderMode]'s answer: a client that means a
// specific mode should use reader.setMode, which refuses a name it does not
// know.
func (p *Preferences) UnmarshalJSON(data []byte) error {
	var w preferencesJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*p = Preferences{
		Mode:                nfc.ParseReaderMode(w.Mode),
		CardTypes:           w.CardTypes,
		DevicePath:          w.DevicePath,
		Port:                w.Port,
		RequirePairedDevice: w.RequirePairedDevice,
		ReaderFeedback:      w.ReaderFeedback,
	}
	return nil
}

// Preferences reports what the agent is set to. The console draws from this
// answer, so a mode switched from the tray shows there without either of them
// telling the other.
func (a *Agent) Preferences() Preferences {
	if a == nil {
		return Preferences{}
	}

	return Preferences{
		Mode:                a.CurrentReaderMode(),
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

	a.settingsMu.Lock()
	a.readerMode = mode
	a.settingsMu.Unlock()

	if reader := a.reader.Load(); reader != nil {
		reader.SetMode(mode)
	}
	a.notifyPreferencesChanged()
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

// SetCardTypeFilter replaces the whole filter, as an operator picking types
// does. An empty list is no filter at all.
func (a *Agent) SetCardTypeFilter(cardTypes []string) {
	next := normalizeCardTypes(cardTypes)
	if sameCardTypes(a.CardTypeFilter(), next) {
		return
	}

	a.cardTypes.replace(next)
	a.notifyPreferencesChanged()
}

// CardTypeFilter lists the allowed card types, sorted. Nil when nothing is
// filtered.
func (a *Agent) CardTypeFilter() []string { return a.cardTypes.list() }

// SetPinnedDevice records the reader the operator chose, empty for auto-detect.
// Recording the choice does not start that reader: selecting one restarts it,
// which is the explicit action's job.
func (a *Agent) SetPinnedDevice(devicePath string) {
	if a.CurrentPinnedDevice() == devicePath {
		return
	}

	a.settingsMu.Lock()
	a.pinnedDevice = devicePath
	a.settingsMu.Unlock()

	a.notifyPreferencesChanged()
}

// CurrentPinnedDevice is the reader the operator chose, empty for auto-detect.
func (a *Agent) CurrentPinnedDevice() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.pinnedDevice
}

// SetDevicePort records the port to serve on. The listener keeps the port it is
// bound on until it is rebound, which ServingPort reports.
func (a *Agent) SetDevicePort(port int) {
	if port <= 0 || port == a.DevicePort() {
		return
	}

	a.settingsMu.Lock()
	a.devicePort = port
	a.settingsMu.Unlock()

	a.notifyPreferencesChanged()
}

// adoptReaderSettings hands a freshly built reader the preferences the agent
// holds. Start creates the reader long after they were set, so without this a
// read-only agent comes back from every restart able to write.
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

// normalizeCardTypes drops blanks and duplicates and sorts what is left, so two
// filters naming the same types compare equal.
func normalizeCardTypes(cardTypes []string) []string {
	if len(cardTypes) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(cardTypes))
	out := make([]string, 0, len(cardTypes))
	for _, cardType := range cardTypes {
		if cardType == "" {
			continue
		}
		if _, dup := seen[cardType]; dup {
			continue
		}
		seen[cardType] = struct{}{}
		out = append(out, cardType)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
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

// notifyPreferencesChanged runs the registered change hooks, so a change made
// from the tray reaches whatever is displaying it.
func (a *Agent) notifyPreferencesChanged() {
	if fn := a.clientsChanged(); fn != nil {
		fn()
	}
}
