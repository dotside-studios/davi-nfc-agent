package nfc

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ebfe/scard"
)

// pcscManager implements Manager using PC/SC via ebfe/scard
type pcscManager struct {
	ctx       *scard.Context
	ctxMu     sync.Mutex
	lastCheck time.Time
}

// newPCSCManager creates a new PC/SC manager
func newPCSCManager() *pcscManager {
	return &pcscManager{}
}

// ensureContext ensures we have a valid PC/SC context
func (m *pcscManager) ensureContext() error {
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()

	if m.ctx != nil {
		// Check if context is still valid by listing readers
		_, err := m.ctx.ListReaders()
		if err == nil {
			return nil
		}
		// Context is invalid, release it
		m.ctx.Release()
		m.ctx = nil
	}

	// Establish new context
	ctx, err := scard.EstablishContext()
	if err != nil {
		return fmt.Errorf("failed to establish PC/SC context: %w", err)
	}
	m.ctx = ctx
	return nil
}

// OpenDevice opens a connection to a reader and waits for a card
func (m *pcscManager) OpenDevice(deviceStr string) (Device, error) {
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
		return nil, &noCardError{ReaderName: readerName}
	}

	// Connect to the reader
	// Use ShareShared to allow other apps to access the reader
	// Use ProtocolAny to let the reader decide the protocol
	card, err := ctx.Connect(readerName, scard.ShareShared, scard.ProtocolAny)
	if err != nil {
		// Check if no card is present (case-insensitive match for various PC/SC error messages)
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "no card") ||
			strings.Contains(errLower, "no smart card") ||
			strings.Contains(errLower, "card is not present") ||
			strings.Contains(errLower, "card not present") {
			return nil, &noCardError{ReaderName: readerName}
		}
		return nil, fmt.Errorf("failed to connect to reader %s: %w", readerName, err)
	}

	// Create device wrapper
	dev, err := newPCSCDevice(ctx, card, readerName)
	if err != nil {
		card.Disconnect(scard.LeaveCard)
		return nil, fmt.Errorf("failed to initialize device: %w", err)
	}

	return dev, nil
}

// isCardPresentLocked checks if a card is present in the reader using GetStatusChange
// with a very short timeout to avoid blocking.
// NOTE: Caller must hold ctxMu lock.
func (m *pcscManager) isCardPresentLocked(ctx *scard.Context, readerName string) (bool, error) {
	// Create reader state for status check
	readerStates := []scard.ReaderState{
		{
			Reader:       readerName,
			CurrentState: scard.StateUnaware,
		},
	}

	// Use a very short timeout (just check current state, don't wait)
	// Timeout of 0 means return immediately with current state
	err := ctx.GetStatusChange(readerStates, 0)
	if err != nil {
		// Timeout is expected - it means no state change, check current state
		errLower := strings.ToLower(err.Error())
		if !strings.Contains(errLower, "timeout") {
			return false, err
		}
	}

	// Check if card is present in the reader
	state := readerStates[0].EventState
	return (state & scard.StatePresent) != 0, nil
}

// ListDevices lists available PC/SC readers
func (m *pcscManager) ListDevices() ([]string, error) {
	var readers []string
	var lastErr error

	for i := 0; i < DeviceEnumRetries; i++ {
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

	return nil, fmt.Errorf("failed to list PC/SC readers after %d retries: %w", DeviceEnumRetries, lastErr)
}

// DeviceChanges returns a channel that signals when devices change
// PC/SC doesn't have a native notification mechanism, so we poll
func (m *pcscManager) DeviceChanges() <-chan struct{} {
	ch := make(chan struct{}, 1)

	go func() {
		var lastReaders []string
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			readers, err := m.ListDevices()
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
func (m *pcscManager) Release() error {
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()

	if m.ctx != nil {
		err := m.ctx.Release()
		m.ctx = nil
		return err
	}
	return nil
}
