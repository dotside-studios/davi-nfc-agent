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
	// it was built with.
	Port int

	RequirePairedDevice bool
	ReaderFeedback      bool

	// AllowRawAPDU opens the raw APDU channel. Off by default: a raw exchange
	// reaches the tag unmodified and can lock or brick it, so it stays refused,
	// even in a writable mode, until this is set.
	AllowRawAPDU bool
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
	AllowRawAPDU        bool     `json:"allowRawApdu"`
}

func (p Preferences) MarshalJSON() ([]byte, error) {
	return json.Marshal(preferencesJSON{
		Mode:                p.Mode.String(),
		CardTypes:           p.CardTypes,
		DevicePath:          p.DevicePath,
		Port:                p.Port,
		RequirePairedDevice: p.RequirePairedDevice,
		ReaderFeedback:      p.ReaderFeedback,
		AllowRawAPDU:        p.AllowRawAPDU,
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
		AllowRawAPDU:        w.AllowRawAPDU,
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

	readers := a.supervisor.Load()

	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.preferencesLocked(readers)
}

// preferencesLocked reads the preferences with settingsMu already held, taking
// the mode and the feedback flag from readers when there is one. The card-type
// filter guards itself, so it is read here rather than passed in.
func (a *Agent) preferencesLocked(readers *nfc.Supervisor) Preferences {
	p := Preferences{
		Mode:                a.readerMode,
		CardTypes:           a.cardTypes.list(),
		DevicePath:          a.pinnedDevice,
		Port:                a.devicePort,
		RequirePairedDevice: a.requirePairedDevice,
		ReaderFeedback:      a.readerFeedback,
		AllowRawAPDU:        a.allowRawAPDU,
	}
	if readers != nil {
		p.Mode = readers.Mode()
		p.ReaderFeedback = readers.FeedbackEnabled()
	}
	return p
}

// ApplyPreferences changes the preferences as one value and answers with what
// the agent holds afterwards, which is not necessarily what was asked for: a
// port of zero or less keeps the current one, and the card types are
// normalized.
//
//	a.ApplyPreferences(func(p *agent.Preferences) { p.Mode = nfc.ModeReadOnly })
//
// Events().Preferences fires once, after every field is in place, so a
// subscriber never sees a combination that was not asked for. Nothing fires
// when the result matches what the agent already held.
//
// mutate runs before the settings lock is taken, so it may call back into the
// agent. Two applies racing can therefore lose one of the two edits; the
// console and the tray each apply from a single goroutine.
//
// Nothing is persisted: a change lasts as long as the agent runs.
func (a *Agent) ApplyPreferences(mutate func(*Preferences)) Preferences {
	if a == nil {
		return Preferences{}
	}

	readers := a.supervisor.Load()

	a.settingsMu.RLock()
	before := a.preferencesLocked(readers)
	a.settingsMu.RUnlock()

	next := before
	next.CardTypes = append([]string(nil), before.CardTypes...)
	if mutate != nil {
		mutate(&next)
	}

	next.CardTypes = normalizeCardTypes(next.CardTypes)
	if next.Port <= 0 {
		next.Port = before.Port
	}

	a.settingsMu.Lock()
	a.readerMode = next.Mode
	a.pinnedDevice = next.DevicePath
	a.devicePort = next.Port
	a.requirePairedDevice = next.RequirePairedDevice
	a.readerFeedback = next.ReaderFeedback
	a.allowRawAPDU = next.AllowRawAPDU
	a.settingsMu.Unlock()

	if !sameCardTypes(before.CardTypes, next.CardTypes) {
		a.cardTypes.replace(next.CardTypes)
	}
	if readers != nil {
		// Both walk every open reader, so they are called only on a change.
		if before.Mode != next.Mode {
			readers.SetMode(next.Mode)
		}
		if before.ReaderFeedback != next.ReaderFeedback {
			readers.SetFeedback(next.ReaderFeedback)
		}
	}

	if !samePreferences(before, next) {
		a.firePreferencesChanged()
	}
	return next
}

// SetReaderMode changes the reader's access mode, on a running reader as well
// as on the next one the agent starts. Accepted with no reader running, because
// the mode is the agent's and Start hands it to the reader it builds.
func (a *Agent) SetReaderMode(mode nfc.ReaderMode) {
	a.ApplyPreferences(func(p *Preferences) { p.Mode = mode })
}

// CurrentReaderMode is the mode the reader is in, or the one the next reader
// will start in while there is none.
func (a *Agent) CurrentReaderMode() nfc.ReaderMode {
	if readers := a.supervisor.Load(); readers != nil {
		return readers.Mode()
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.readerMode
}

// SetCardTypeFilter replaces the whole filter, as an operator picking types
// does. An empty list is no filter at all.
func (a *Agent) SetCardTypeFilter(cardTypes []string) {
	a.ApplyPreferences(func(p *Preferences) { p.CardTypes = cardTypes })
}

// CardTypeFilter lists the allowed card types, sorted. Nil when nothing is
// filtered.
func (a *Agent) CardTypeFilter() []string { return a.cardTypes.list() }

// SetPinnedDevice records the reader the operator chose, empty for auto-detect.
// It filters what the agent reports rather than choosing what is opened: every
// reader the manager offers stays open.
func (a *Agent) SetPinnedDevice(devicePath string) {
	a.ApplyPreferences(func(p *Preferences) { p.DevicePath = devicePath })
}

// pinAdmits reports whether the pin lets this agent work with a source. It
// decides both what the agent reports and what it operates on, so a client is
// not offered a tag it cannot reach or handed one it was never shown.
//
// The pin is a filter rather than a lock: the readers stay open and a scan from
// one the operator is not asking for is dropped, wherever it was read.
//
// Only readers are filtered. A device that reports its own scans, such as a
// phone, is not one the agent chose to read from, so pinning a reader says
// nothing about it.
func (a *Agent) pinAdmits(device string) bool {
	pinned := a.CurrentPinnedDevice()
	if pinned == "" || device == "" || device == pinned {
		return true
	}

	readers := a.supervisor.Load()
	return readers == nil || !readers.Operates(device)
}

// CurrentPinnedDevice is the reader the operator chose, empty for auto-detect.
func (a *Agent) CurrentPinnedDevice() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.pinnedDevice
}

// SetDevicePort records the port to serve on. A listener keeps the port it was
// built with, so what is being served on is [ServerPlugin.Port].
func (a *Agent) SetDevicePort(port int) {
	a.ApplyPreferences(func(p *Preferences) { p.Port = port })
}

// adoptReaderSettings hands the readers the preferences the agent holds. Start
// opens them long after those were set, so without this a read-only agent comes
// back from every restart able to write.
func (a *Agent) adoptReaderSettings() {
	readers := a.supervisor.Load()
	if readers == nil {
		return
	}

	a.settingsMu.RLock()
	mode, feedback := a.readerMode, a.readerFeedback
	a.settingsMu.RUnlock()

	readers.SetMode(mode)
	readers.SetFeedback(feedback)
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

// samePreferences compares two snapshots, both with normalized card types. It
// is what decides whether an apply reports a change.
func samePreferences(a, b Preferences) bool {
	return a.Mode == b.Mode &&
		a.DevicePath == b.DevicePath &&
		a.Port == b.Port &&
		a.RequirePairedDevice == b.RequirePairedDevice &&
		a.ReaderFeedback == b.ReaderFeedback &&
		a.AllowRawAPDU == b.AllowRawAPDU &&
		sameCardTypes(a.CardTypes, b.CardTypes)
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
