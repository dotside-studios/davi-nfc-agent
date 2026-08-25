package nfc

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
)

// Supervisor operates every reader a manager offers, rather than one chosen at
// startup. It opens each, polls it, and publishes what they scan on one signal,
// with each scan naming the reader it came from.
//
// A reader plugged in while it runs is picked up; one unplugged is dropped.
// Operations name the reader they apply to, so two of them do not queue behind
// each other.
//
// It is safe to use from any goroutine, and reports whether or not anything is
// listening.
type Supervisor struct {
	manager Manager
	timeout time.Duration

	mu      sync.Mutex
	readers map[string]*deviceReader
	started bool
	done    bool

	// The policy every reader runs under, held here as well as on the readers
	// so one opened later starts under it too.
	mode        ReaderMode
	feedback    bool
	classicKeys [][]byte

	scans  event.Signal[NFCData]
	status event.Signal[DeviceStatus]

	stopped chan struct{}
	wg      sync.WaitGroup
}

// NewSupervisor returns a supervisor over the readers manager offers. opTimeout
// bounds a single tag operation; zero takes the reader's default.
func NewSupervisor(manager Manager, opTimeout time.Duration) (*Supervisor, error) {
	if manager == nil {
		return nil, fmt.Errorf("nfc: a supervisor needs a manager")
	}

	return &Supervisor{
		manager: manager,
		timeout: opTimeout,
		readers: make(map[string]*deviceReader),
		mode:    ModeReadWrite,
		stopped: make(chan struct{}),
	}, nil
}

// Scans carries every tag the readers report, each naming the reader that read
// it.
func (s *Supervisor) Scans() *event.Signal[NFCData] { return &s.scans }

// Status carries what each reader reports about itself: whether it is
// connected, and whether a card is on it.
func (s *Supervisor) Status() *event.Signal[DeviceStatus] { return &s.status }

// Start opens the readers the manager offers and begins polling them. It is an
// error to start one twice.
//
// A reader that cannot be opened is logged and left out rather than failing the
// rest: one held by another process should not stop the others working.
func (s *Supervisor) Start() error {
	s.mu.Lock()
	switch {
	case s.done:
		s.mu.Unlock()
		return fmt.Errorf("nfc: the supervisor has been stopped; build another")
	case s.started:
		s.mu.Unlock()
		return fmt.Errorf("nfc: the supervisor is already started")
	}
	s.started = true
	s.mu.Unlock()

	s.reconcile()
	s.watchDevices()
	return nil
}

// Stop ends every reader and releases the devices behind them. The supervisor
// cannot be started again.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	s.done = true
	readers := s.readers
	s.readers = make(map[string]*deviceReader)
	s.mu.Unlock()

	close(s.stopped)
	for _, reader := range readers {
		reader.Stop()
		reader.Close()
	}
	s.wg.Wait()
}

// Devices lists the readers the supervisor is operating.
func (s *Supervisor) Devices() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.readers))
	for device := range s.readers {
		out = append(out, device)
	}
	sort.Strings(out)
	return out
}

// reconcile opens readers the manager has gained and drops the ones it has
// lost, leaving the rest running.
func (s *Supervisor) reconcile() {
	devices, err := ListReaders(s.manager)
	if err != nil {
		log.Printf("[supervisor] Listing readers failed: %v", err)
		return
	}

	wanted := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		wanted[device] = struct{}{}
	}

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}

	var dropped []*deviceReader
	for device, reader := range s.readers {
		if _, keep := wanted[device]; !keep {
			dropped = append(dropped, reader)
			delete(s.readers, device)
		}
	}

	var opened []string
	for device := range wanted {
		if _, have := s.readers[device]; !have {
			opened = append(opened, device)
		}
	}
	mode, feedback, keys := s.mode, s.feedback, s.classicKeys
	s.mu.Unlock()

	for _, reader := range dropped {
		reader.Stop()
		reader.Close()
	}

	for _, device := range opened {
		reader, err := newDeviceReader(device, s.manager, s.timeout)
		if err != nil {
			log.Printf("[supervisor] Cannot open reader %s: %v", device, err)
			continue
		}
		reader.SetMode(mode)
		reader.SetFeedback(feedback)
		reader.SetClassicKeys(keys)

		s.mu.Lock()
		if !s.started {
			s.mu.Unlock()
			reader.Close()
			return
		}
		s.readers[device] = reader
		s.mu.Unlock()

		reader.Start()
		s.wg.Add(1)
		go s.publish(reader)
	}
}

// publish forwards one reader's scans and status until the supervisor stops.
func (s *Supervisor) publish(reader *deviceReader) {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopped:
			return
		case data := <-reader.Data():
			s.scans.Emit(data)
		case status := <-reader.StatusUpdates():
			s.status.Emit(status)
		}
	}
}

// watchDevices reconciles whenever the manager reports its device set changed.
func (s *Supervisor) watchDevices() {
	notifier, ok := s.manager.(DeviceChangeNotifier)
	if !ok {
		return
	}

	changes := notifier.DeviceChanges()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.stopped:
				return
			case _, ok := <-changes:
				if !ok {
					return
				}
				s.reconcile()
			}
		}
	}()
}
