package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
)

// originsFileName holds the persisted origin allowlist, beside the API secret.
const originsFileName = "allowed-origins.json"

// defaultAllowedOrigins are the first-party consoles this agent exists to serve.
// They ship allowed so the shipped console works on a fresh install without
// configuration — the guard is there to stop arbitrary sites, not our own.
//
// Local ports cover a developer running a console from source.
var defaultAllowedOrigins = []string{
	"davi.social",
	"shop.davi.social",
	"localhost:3000",
	"localhost:3002",
}

// OriginStore is the agent's allowlist: the persisted set, plus any origin
// allowed for this run only, plus the rejections the tray offers to allow.
//
// It is consulted on every upgrade, so reads are cheap and writes are rare.
type OriginStore struct {
	mu sync.RWMutex

	configDir string
	persisted map[string]struct{}

	// sessionAllowAny disables the check until the agent restarts. Deliberately
	// never persisted: left on by accident it would let any site the operator
	// visits drive the reader, including locking cards irreversibly.
	sessionAllowAny bool

	// blocked records origins rejected since start, so the tray can offer them.
	blocked      []string
	onBlocked    func(origin string)
	onChangeFunc func()
}

// NewOriginStore loads the allowlist from configDir, seeding it with the
// first-party defaults the first time.
func NewOriginStore(configDir string) (*OriginStore, error) {
	s := &OriginStore{
		configDir: configDir,
		persisted: make(map[string]struct{}),
	}

	if configDir == "" {
		s.seedDefaults()
		return s, nil
	}

	path := filepath.Join(configDir, originsFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s.seedDefaults()
		return s, s.save()
	}
	if err != nil {
		return nil, fmt.Errorf("read origins file: %w", err)
	}

	var stored []string
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse origins file: %w", err)
	}
	for _, origin := range stored {
		if origin = normalizeOrigin(origin); origin != "" {
			s.persisted[origin] = struct{}{}
		}
	}
	return s, nil
}

func (s *OriginStore) seedDefaults() {
	for _, origin := range defaultAllowedOrigins {
		s.persisted[origin] = struct{}{}
	}
}

// OnBlocked registers a callback for rejected origins, so the tray can offer to
// allow one instead of leaving the operator with an unexplained failure.
func (s *OriginStore) OnBlocked(fn func(origin string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onBlocked = fn
}

// OnChange registers a callback fired whenever the allowlist changes.
func (s *OriginStore) OnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChangeFunc = fn
}

// Allowed reports whether an origin may connect.
func (s *OriginStore) Allowed(origin string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionAllowAny {
		return true
	}
	_, ok := s.persisted[normalizeOrigin(origin)]
	return ok
}

// RecordBlocked notes a rejected origin and notifies any listener. Repeats are
// collapsed so a console retrying in a loop does not flood the menu.
func (s *OriginStore) RecordBlocked(origin string) {
	origin = normalizeOrigin(origin)
	if origin == "" {
		return
	}

	s.mu.Lock()
	for _, existing := range s.blocked {
		if existing == origin {
			s.mu.Unlock()
			return
		}
	}
	s.blocked = append(s.blocked, origin)
	notify := s.onBlocked
	s.mu.Unlock()

	if notify != nil {
		notify(origin)
	}
}

// Blocked returns origins rejected since start that are not now allowed.
func (s *OriginStore) Blocked() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []string
	for _, origin := range s.blocked {
		if _, ok := s.persisted[origin]; !ok {
			out = append(out, origin)
		}
	}
	sort.Strings(out)
	return out
}

// List returns the allowed origins, sorted.
func (s *OriginStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.persisted))
	for origin := range s.persisted {
		out = append(out, origin)
	}
	sort.Strings(out)
	return out
}

// Allow adds an origin and persists it.
func (s *OriginStore) Allow(origin string) error {
	origin = normalizeOrigin(origin)
	if origin == "" {
		return fmt.Errorf("empty origin")
	}

	s.mu.Lock()
	s.persisted[origin] = struct{}{}
	err := s.saveLocked()
	notify := s.onChangeFunc
	s.mu.Unlock()

	if notify != nil {
		notify()
	}
	return err
}

// Revoke removes an origin and persists the removal.
func (s *OriginStore) Revoke(origin string) error {
	origin = normalizeOrigin(origin)

	s.mu.Lock()
	delete(s.persisted, origin)
	err := s.saveLocked()
	notify := s.onChangeFunc
	s.mu.Unlock()

	if notify != nil {
		notify()
	}
	return err
}

// SessionAllowAny turns the origin check off for this run only. It is not
// persisted, so a restart restores the guard.
func (s *OriginStore) SessionAllowAny(on bool) {
	s.mu.Lock()
	s.sessionAllowAny = on
	notify := s.onChangeFunc
	s.mu.Unlock()

	if notify != nil {
		notify()
	}
}

// IsSessionAllowAny reports whether the check is off for this run.
func (s *OriginStore) IsSessionAllowAny() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionAllowAny
}

func (s *OriginStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *OriginStore) saveLocked() error {
	if s.configDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.configDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	_ = tlspkg.SecureDir(s.configDir)

	origins := make([]string, 0, len(s.persisted))
	for origin := range s.persisted {
		origins = append(origins, origin)
	}
	sort.Strings(origins)

	data, err := json.MarshalIndent(origins, "", "  ")
	if err != nil {
		return fmt.Errorf("encode origins: %w", err)
	}

	path := filepath.Join(s.configDir, originsFileName)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write origins file: %w", err)
	}
	_ = tlspkg.SecureFile(path)
	return nil
}

// normalizeOrigin reduces an origin to the host:port CheckOrigin matches on, so
// a pasted URL and a bare host are the same entry.
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" || origin == "*" {
		return origin
	}
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	if i := strings.IndexAny(origin, "/?#"); i >= 0 {
		origin = origin[:i]
	}
	return strings.ToLower(origin)
}
