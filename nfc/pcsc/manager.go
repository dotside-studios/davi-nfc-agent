// Package pcsc drives the NFC readers attached to this machine through the
// platform's PC/SC stack. It implements nfc.Manager and nfc.Device, so the rest
// of the agent works a reader without knowing PC/SC exists.
//
// The PC/SC calls go through a small adapter (scard.go) with two interchangeable
// backends. The default is goscard, which resolves the platform's library
// (winscard.dll, libpcsclite.so.1, PCSC.framework) at runtime through purego, so
// the agent needs neither cgo nor a C toolchain. Building with -tags cgopcsc
// selects the ebfe/scard binding instead; Backend reports which one a binary
// carries.
package pcsc

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Manager implements nfc.Manager for PC/SC readers.
type Manager struct {
	ctx   scardContext
	ctxMu sync.Mutex
}

// NewManager creates a manager for the PC/SC readers attached to this machine.
//
// Example:
//
//	manager := NewManager()
func NewManager() *Manager {
	return &Manager{}
}

// ensureContext ensures we have a valid PC/SC context
func (m *Manager) ensureContext() error {
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()

	if m.ctx != nil {
		// Check if context is still valid by listing readers
		_, err := m.ctx.ListReaders()
		if err == nil {
			return nil
		}
		// scardContext is invalid, release it; the release error is moot because
		// we replace the context below either way.
		_ = m.ctx.Release()
		m.ctx = nil
	}

	// Establish new context
	ctx, err := establishContext()
	if err != nil {
		return fmt.Errorf("failed to establish PC/SC context: %w", err)
	}
	m.ctx = ctx
	return nil
}

// OpenDevice opens a connection to a reader and waits for a card
func (m *Manager) OpenDevice(deviceStr string) (nfc.Device, error) {
	if err := m.ensureContext(); err != nil {
		return nil, err
	}

	// Hold the lock for all context operations to prevent the context from being
	// released by another goroutine calling ensureContext() while we're using it.
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()

	ctx := m.ctx
	if ctx == nil {
		return nil, fmt.Errorf("PC/SC context is nil")
	}

	// If no device specified, use the first available reader
	readerName := deviceStr
	if readerName == "" {
		readers, err := ctx.ListReaders()
		if err != nil {
			return nil, fmt.Errorf("failed to list readers: %w", err)
		}

		// Filter to contactless readers
		readers = filterContactlessReaders(readers)
		if len(readers) == 0 {
			return nil, fmt.Errorf("no PC/SC readers found")
		}

		readerName = readers[0]
	}

	// Check if a card is present before attempting to connect
	// This prevents blocking on Connect() when no card is present
	cardPresent, err := m.isCardPresentLocked(ctx, readerName)
	if err != nil {
		return nil, fmt.Errorf("failed to check card presence: %w", err)
	}
	if !cardPresent {
		return nil, nfc.NewNoCardError(readerName)
	}

	// Connect to the reader
	// Use shareShared to allow other apps to access the reader
	// Use protocolAny to let the reader decide the protocol
	card, err := ctx.Connect(readerName, shareShared, protocolAny)
	if err != nil {
		// Check if no card is present. The status code is the reliable signal;
		// the string matches stay as a fallback for readers whose drivers
		// report something else.
		errLower := strings.ToLower(err.Error())
		if errors.Is(err, errNoSmartcard) ||
			strings.Contains(errLower, "no card") ||
			strings.Contains(errLower, "no smart card") ||
			strings.Contains(errLower, "card is not present") ||
			strings.Contains(errLower, "card not present") {
			return nil, nfc.NewNoCardError(readerName)
		}
		return nil, fmt.Errorf("failed to connect to reader %s: %w", readerName, err)
	}

	// Create device wrapper
	dev, err := newDevice(ctx, card, readerName)
	if err != nil {
		_ = card.Disconnect(leaveCard)
		return nil, fmt.Errorf("failed to initialize device: %w", err)
	}

	return dev, nil
}

// isCardPresentLocked checks if a card is present in the reader using GetStatusChange
// with a very short timeout to avoid blocking.
// NOTE: Caller must hold ctxMu lock.
func (m *Manager) isCardPresentLocked(ctx scardContext, readerName string) (bool, error) {
	// Create reader state for status check
	readerStates := []readerState{
		{
			Reader:       readerName,
			CurrentState: stateUnaware,
		},
	}

	// Use a very short timeout (just check current state, don't wait)
	// Timeout of 0 means return immediately with current state
	err := ctx.GetStatusChange(readerStates, 0)
	if err != nil {
		// Timeout is expected - it means no state change, check current state
		if !errors.Is(err, errTimeout) {
			return false, err
		}
	}

	// Check if card is present in the reader
	state := readerStates[0].EventState
	return (state & statePresent) != 0, nil
}

// readerTransport is what any PC/SC reader can do: it is opened, polled and
// spoken to directly.
var readerTransport = nfc.DeviceCapabilities{
	CanPoll:       true,
	CanTransceive: true,
	DeviceType:    "pcsc",
}

// Devices lists the contactless readers PC/SC offers.
func (m *Manager) Devices() ([]nfc.DeviceListing, error) {
	readers, err := m.listReaders()
	if err != nil {
		return nil, err
	}

	listings := make([]nfc.DeviceListing, 0, len(readers))
	for _, reader := range readers {
		listings = append(listings, nfc.DeviceListing{Path: reader, ID: reader, Capabilities: readerTransport})
	}
	return listings, nil
}

func (m *Manager) listReaders() ([]string, error) {
	var readers []string
	var lastErr error

	for i := 0; i < nfc.DeviceEnumRetries; i++ {
		if err := m.ensureContext(); err != nil {
			lastErr = err
			time.Sleep(time.Millisecond * 100)
			continue
		}

		// Hold the lock while using the context to prevent it from being
		// released by another goroutine calling ensureContext().
		m.ctxMu.Lock()
		ctx := m.ctx
		if ctx == nil {
			m.ctxMu.Unlock()
			lastErr = fmt.Errorf("PC/SC context is nil")
			time.Sleep(time.Millisecond * 100)
			continue
		}

		var err error
		readers, err = ctx.ListReaders()
		m.ctxMu.Unlock()

		if err != nil {
			lastErr = err
			time.Sleep(time.Millisecond * 100)
			continue
		}

		// Filter to contactless readers
		readers = filterContactlessReaders(readers)
		return readers, nil
	}

	return nil, fmt.Errorf("failed to list PC/SC readers after %d retries: %w", nfc.DeviceEnumRetries, lastErr)
}

// DeviceChanges returns a channel that signals when devices change
// PC/SC doesn't have a native notification mechanism, so we poll
func (m *Manager) DeviceChanges() <-chan struct{} {
	ch := make(chan struct{}, 1)

	go func() {
		var lastReaders []string
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			readers, err := m.listReaders()
			if err != nil {
				continue
			}

			// Check if reader list changed
			if !stringSlicesEqual(readers, lastReaders) {
				lastReaders = readers
				select {
				case ch <- struct{}{}:
				default:
					// Channel full, skip notification
				}
			}
		}
	}()

	return ch
}

// stringSlicesEqual compares two string slices for equality
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// Release releases the PC/SC context
func (m *Manager) Release() error {
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()

	if m.ctx != nil {
		err := m.ctx.Release()
		m.ctx = nil
		return err
	}
	return nil
}
