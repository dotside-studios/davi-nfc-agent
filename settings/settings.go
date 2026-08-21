package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

const fileName = "settings.json"

// Settings holds the operator preferences that survive a restart.
//
// Credentials are deliberately not here: the API secret, paired devices and
// origin allowlist keep their own files.
type Settings struct {
	// Mode is the reader access mode: "readwrite", "read" or "write".
	Mode string `json:"mode"`

	// CardTypes is the card-type allowlist. Empty means every type is allowed,
	// matching the agent's behaviour when no filter has ever been set.
	CardTypes []string `json:"cardTypes"`

	// DevicePath pins a specific reader. Empty means auto-detect.
	DevicePath string `json:"devicePath"`

	// Port is the agent server port. Zero means the built-in default.
	//
	// Applied at startup only: changing it needs the listener rebound, which
	// the console does explicitly rather than as a side effect of saving.
	Port int `json:"port"`

	// RequirePairedDevice admits only devices holding a paired credential.
	RequirePairedDevice bool `json:"requirePairedDevice"`

	// ReaderFeedback has the reader flash its LED and sound its buzzer when a
	// tag is read or written. Off by default, since a reader sitting beside
	// someone all day stays quiet unless its operator asks otherwise.
	ReaderFeedback bool `json:"readerFeedback"`
}

// Defaults are what the agent does when no file has ever been saved.
func Defaults() Settings {
	return Settings{
		Mode:      ModeReadWrite,
		CardTypes: nil,
	}
}

// Reader mode identifiers as they appear on the wire and on disk. nfc's
// ReaderMode is an iota, so persisting it directly would reinterpret stored
// files if a mode were ever inserted.
const (
	ModeReadWrite = "readwrite"
	ModeReadOnly  = "read"
	ModeWriteOnly = "write"
)

// ParseMode converts a stored identifier to a nfc.ReaderMode, falling back to
// read/write so a file from a newer build cannot leave the reader unusable.
func ParseMode(mode string) nfc.ReaderMode {
	switch mode {
	case ModeReadOnly:
		return nfc.ModeReadOnly
	case ModeWriteOnly:
		return nfc.ModeWriteOnly
	default:
		return nfc.ModeReadWrite
	}
}

// FormatMode converts a nfc.ReaderMode to its stored identifier.
func FormatMode(mode nfc.ReaderMode) string {
	switch mode {
	case nfc.ModeReadOnly:
		return ModeReadOnly
	case nfc.ModeWriteOnly:
		return ModeWriteOnly
	default:
		return ModeReadWrite
	}
}

// Store persists Settings under the config directory.
type Store struct {
	mu        sync.RWMutex
	configDir string
	settings  Settings

	onChangeFunc func(Settings)
}

// New loads settings from configDir, falling back to the defaults.
// No file is written on load, so its absence keeps meaning "never configured".
// An empty configDir yields an in-memory store.
func New(configDir string) (*Store, error) {
	s := &Store{
		configDir: configDir,
		settings:  Defaults(),
	}

	if configDir == "" {
		return s, nil
	}

	data, err := os.ReadFile(filepath.Join(configDir, fileName))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings file: %w", err)
	}

	// Over the defaults, so a field absent from an older file keeps its default.
	stored := Defaults()
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse settings file: %w", err)
	}

	s.settings = normalize(stored)
	return s, nil
}

// OnChange registers a callback fired whenever the settings change.
func (s *Store) OnChange(fn func(Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChangeFunc = fn
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.clone()
}

// Save replaces the settings and persists them, normalizing first.
func (s *Store) Save(next Settings) error {
	s.mu.Lock()
	s.settings = normalize(next)
	saved := s.settings.clone()
	onChange := s.onChangeFunc
	err := s.saveLocked()
	s.mu.Unlock()

	if err != nil {
		return err
	}
	if onChange != nil {
		onChange(saved)
	}
	return nil
}

// Update applies a mutation and persists the result, without the caller having
// to read-modify-write around the lock.
func (s *Store) Update(mutate func(*Settings)) (Settings, error) {
	s.mu.Lock()
	next := s.settings.clone()
	mutate(&next)
	s.settings = normalize(next)
	saved := s.settings.clone()
	onChange := s.onChangeFunc
	err := s.saveLocked()
	s.mu.Unlock()

	if err != nil {
		return saved, err
	}
	if onChange != nil {
		onChange(saved)
	}
	return saved, nil
}

// saveLocked writes the settings file. Caller holds the write lock.
func (s *Store) saveLocked() error {
	if s.configDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')

	// Write and rename, so an interrupted write leaves no truncated file.
	path := filepath.Join(s.configDir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write settings file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}

// Path returns the settings file location, or "" for an in-memory store.
func (s *Store) Path() string {
	if s.configDir == "" {
		return ""
	}
	return filepath.Join(s.configDir, fileName)
}

func (s Settings) clone() Settings {
	out := s
	out.CardTypes = append([]string(nil), s.CardTypes...)
	return out
}

// normalize coerces settings into a form the agent can apply.
func normalize(s Settings) Settings {
	switch s.Mode {
	case ModeReadWrite, ModeReadOnly, ModeWriteOnly:
	default:
		s.Mode = ModeReadWrite
	}

	if len(s.CardTypes) > 0 {
		known := make(map[string]struct{}, len(nfc.GetAllCardTypes()))
		for _, t := range nfc.GetAllCardTypes() {
			known[t] = struct{}{}
		}

		seen := make(map[string]struct{}, len(s.CardTypes))
		filtered := make([]string, 0, len(s.CardTypes))
		for _, t := range s.CardTypes {
			if _, ok := known[t]; !ok {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			filtered = append(filtered, t)
		}
		sort.Strings(filtered)

		// Naming every type is the same as no filter; store it that way so
		// "unfiltered" has one representation.
		if len(filtered) == len(known) {
			filtered = nil
		}
		s.CardTypes = filtered
	} else {
		s.CardTypes = nil
	}

	if s.Port < 0 || s.Port > 65535 {
		s.Port = 0
	}

	return s
}
