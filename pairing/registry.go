package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/google/uuid"
)

// devicesFileName holds the paired-device registry, beside the API secret.
const devicesFileName = "paired-devices.json"

// Device is one device that completed pairing.
//
// The token is stored only as a hash. It is shown once, at pairing, and cannot
// be recovered afterwards: a registry that can hand back every device's
// credential is a single file worth stealing.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	TokenHash string    `json:"tokenHash"`
	PairedAt  time.Time `json:"pairedAt"`
	LastSeen  time.Time `json:"lastSeen,omitempty"`
}

// Registry holds the paired devices and their credentials.
//
// It exists so a device can be revoked on its own. The shared API secret it
// supplements is all-or-nothing: rotating it to remove one phone logs out every
// other device at the same time.
type Registry struct {
	mu        sync.RWMutex
	configDir string
	devices   map[string]*Device // id -> device

	// changed carries the registry after every pairing and revocation.
	changed event.Signal[[]Device]

	// revoked carries the IDs whose credentials just stopped being valid.
	// Separate from changed because a subscriber acting on a revocation needs
	// to know who was revoked, and the registry no longer holds them.
	revoked event.Signal[[]string]
}

// NewRegistry loads the registry from configDir. An empty configDir keeps the
// devices in memory, which is what a build with nowhere to persist them gets.
func NewRegistry(configDir string) (*Registry, error) {
	r := &Registry{
		configDir: configDir,
		devices:   make(map[string]*Device),
	}

	if configDir == "" {
		return r, nil
	}

	data, err := os.ReadFile(filepath.Join(configDir, devicesFileName))
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read devices file: %w", err)
	}

	var stored []*Device
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse devices file: %w", err)
	}
	for _, device := range stored {
		if device.ID != "" {
			r.devices[device.ID] = device
		}
	}
	return r, nil
}

// OnChange registers fn to run when a device is paired or revoked, and returns
// the handle that removes it again.
func (r *Registry) OnChange(fn func()) *event.Connection {
	if fn == nil {
		return nil
	}
	return r.changed.Connect(func([]Device) { fn() })
}

// OnRevoke registers fn to run with the IDs whose credentials were just
// revoked, and returns the handle that removes it again.
//
// A token is only checked when a device connects, so revoking one does nothing
// to a device already connected. Anything holding live sessions subscribes here
// and ends the matching one.
func (r *Registry) OnRevoke(fn func(ids []string)) *event.Connection {
	if fn == nil {
		return nil
	}
	return r.revoked.Connect(fn)
}

// notifyChanged publishes the registry. Called with the lock released, since a
// handler reads the registry back.
func (r *Registry) notifyChanged() { r.changed.Emit(r.List()) }

// Pair registers a device and returns its token. The token is returned exactly
// once; only its hash is kept.
func (r *Registry) Pair(name, platform string) (*Device, string, error) {
	if name == "" {
		name = "Paired device"
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, "", fmt.Errorf("generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])

	device := &Device{
		ID:        uuid.NewString(),
		Name:      name,
		Platform:  platform,
		TokenHash: hashToken(token),
		PairedAt:  time.Now().UTC(),
	}

	r.mu.Lock()
	r.devices[device.ID] = device
	err := r.saveLocked()
	r.mu.Unlock()

	r.notifyChanged()
	if err != nil {
		return nil, "", err
	}
	return device, token, nil
}

// VerifyToken reports whether a presented token belongs to a paired device.
func (r *Registry) VerifyToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	want := hashToken(token)

	r.mu.RLock()
	var matched *Device
	for _, device := range r.devices {
		// Constant-time throughout: a token that shares a prefix with a real
		// one must not take measurably longer to reject.
		if subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(want)) == 1 {
			matched = device
		}
	}
	r.mu.RUnlock()

	if matched == nil {
		return "", false
	}

	r.mu.Lock()
	matched.LastSeen = time.Now().UTC()
	r.mu.Unlock()

	return matched.ID, true
}

// List returns the paired devices, most recently paired first.
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Device, 0, len(r.devices))
	for _, device := range r.devices {
		out = append(out, *device)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PairedAt.After(out[j].PairedAt)
	})
	return out
}

// Count returns how many devices are paired.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.devices)
}

// Revoke removes a device. Its token stops working immediately, and any session
// it already holds ends with it where something has subscribed to OnRevoke;
// every other device is unaffected.
func (r *Registry) Revoke(id string) error {
	r.mu.Lock()
	_, existed := r.devices[id]
	delete(r.devices, id)
	err := r.saveLocked()
	r.mu.Unlock()

	if !existed {
		return fmt.Errorf("no such device: %s", id)
	}
	r.revoked.Emit([]string{id})
	r.notifyChanged()
	return err
}

// RevokeAll clears the registry, for a machine changing hands.
func (r *Registry) RevokeAll() error {
	r.mu.Lock()
	ids := make([]string, 0, len(r.devices))
	for id := range r.devices {
		ids = append(ids, id)
	}
	r.devices = make(map[string]*Device)
	err := r.saveLocked()
	r.mu.Unlock()

	if len(ids) > 0 {
		r.revoked.Emit(ids)
	}
	r.notifyChanged()
	return err
}

func (r *Registry) saveLocked() error {
	if r.configDir == "" {
		return nil
	}
	if err := os.MkdirAll(r.configDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	_ = tlspkg.SecureDir(r.configDir)

	devices := make([]*Device, 0, len(r.devices))
	for _, device := range r.devices {
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })

	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return fmt.Errorf("encode devices: %w", err)
	}

	path := filepath.Join(r.configDir, devicesFileName)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write devices file: %w", err)
	}
	_ = tlspkg.SecureFile(path)
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
