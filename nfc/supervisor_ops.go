package nfc

import (
	"fmt"
	"sort"
)

// The policy every reader runs under. It is the supervisor's rather than each
// reader's, so a reader opened later starts under the same one.

// SetMode changes what the readers are allowed to do, now and for any reader
// opened afterwards.
func (s *Supervisor) SetMode(mode ReaderMode) {
	s.mu.Lock()
	s.mode = mode
	readers := s.readerList()
	s.mu.Unlock()

	for _, reader := range readers {
		reader.SetMode(mode)
	}
}

// Mode is what the readers are allowed to do.
func (s *Supervisor) Mode() ReaderMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// SetFeedback turns the readers' own light and sound on or off.
func (s *Supervisor) SetFeedback(on bool) {
	s.mu.Lock()
	s.feedback = on
	readers := s.readerList()
	s.mu.Unlock()

	for _, reader := range readers {
		reader.SetFeedback(on)
	}
}

// FeedbackEnabled reports whether the readers answer for their own work.
func (s *Supervisor) FeedbackEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.feedback
}

// SetClassicKeys configures the MIFARE Classic keys every reader tries.
func (s *Supervisor) SetClassicKeys(keys [][]byte) {
	s.mu.Lock()
	s.classicKeys = keys
	readers := s.readerList()
	s.mu.Unlock()

	for _, reader := range readers {
		reader.SetClassicKeys(keys)
	}
}

// Tag on a reader.

// The supervisor answers what the tag router asks of any source. A reader holds
// the tag on it, as a phone holds the one its user tapped, so an operation
// routes to either without the caller learning which. See [TagHolder].

var _ TagHolder = (*Supervisor)(nil)

// TagOn reports the tag a reader is holding, by UID. An empty device asks a
// reader with a card on it.
//
// The answer is the reader's last scan rather than a fresh read: it selects the
// reader, and the reader checks the tag it actually has when it performs the
// operation.
func (s *Supervisor) TagOn(device string) (holder, uid string, ok bool) {
	if device == "" {
		device, ok = s.Present()
		if !ok {
			return "", "", false
		}
	}

	s.mu.Lock()
	reader, known := s.readers[device]
	s.mu.Unlock()
	if !known {
		return "", "", false
	}

	uid = reader.GetLastScannedData()
	if uid == "" {
		return "", "", false
	}
	return device, uid, true
}

// DevicesHoldingTags lists the readers that have a tag to name. A reader's last
// scan stands until it reports another, so a route picked from it is confirmed
// by the reader when the operation runs.
func (s *Supervisor) DevicesHoldingTags() []string {
	var holding []string
	for _, name := range s.readerNames() {
		if _, _, ok := s.TagOn(name); ok {
			holding = append(holding, name)
		}
	}
	return holding
}

// WriteTag encodes msg onto the tag on the named reader. The reader reads it
// back where the tag allows, so the result says whether it was confirmed.
//
// The idempotency key is a device's, not a reader's: a reader is asked once and
// answers for what it did.
func (s *Supervisor) WriteTag(device, tagUID string, msg *NDEFMessage, lock bool, _ string) (*WriteResult, error) {
	return s.WriteMessage(device, msg, WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      lock,
		ExpectUID: tagUID,
	})
}

// LockTag makes the tag on the named reader permanently read-only.
func (s *Supervisor) LockTag(device, tagUID, _ string) (*LockResult, error) {
	return s.Lock(device, tagUID)
}

// TransceiveTag exchanges raw bytes with the tag on the named reader. A reader
// speaks to the tag directly, so raw is what it always is here.
func (s *Supervisor) TransceiveTag(device, tagUID string, data []byte, _ bool) ([]byte, error) {
	return s.Transceive(device, data, tagUID)
}

// TagCapabilities reports what the tag on the named reader supports.
func (s *Supervisor) TagCapabilities(device, tagUID string) (*TagCapabilities, error) {
	return s.Capabilities(device, tagUID)
}

// Operates reports whether this is a reader the supervisor opened, as opposed
// to a device that reports its own scans.
func (s *Supervisor) Operates(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.readers[device]
	return ok
}

// Present reports a reader with a card on it, for a request that named no tag.
func (s *Supervisor) Present() (device string, ok bool) {
	for _, name := range s.readerNames() {
		s.mu.Lock()
		reader := s.readers[name]
		s.mu.Unlock()
		if reader != nil && reader.GetDeviceStatus().CardPresent {
			return name, true
		}
	}
	return "", false
}

// Any names a reader to fall back on when none is holding a card, so a request
// that has to go somewhere reaches one rather than being refused for having no
// route.
func (s *Supervisor) Any() (device string, ok bool) {
	names := s.readerNames()
	if len(names) == 0 {
		return "", false
	}
	return names[0], true
}

// Operations. Each names the reader it applies to, so one reader's operation
// does not queue behind another's.

// WriteMessage encodes a message onto the tag on the named reader.
func (s *Supervisor) WriteMessage(device string, msg *NDEFMessage, opts WriteOptions) (*WriteResult, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.WriteMessageWithResult(msg, opts)
}

// Lock makes the tag on the named reader read-only. expectUID refuses a tag
// other than the one the caller named.
func (s *Supervisor) Lock(device, expectUID string) (*LockResult, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.LockCardExpecting(expectUID)
}

// Transceive exchanges raw bytes with the tag on the named reader.
func (s *Supervisor) Transceive(device string, data []byte, expectUID string) ([]byte, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.TransceiveExpecting(data, expectUID)
}

// Capabilities reports what the tag on the named reader supports.
func (s *Supervisor) Capabilities(device, expectUID string) (*TagCapabilities, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.GetCapabilitiesExpecting(expectUID)
}

// readerFor resolves the reader an operation applies to. An empty device names
// the only reader there is, and is ambiguous once there is more than one: a
// caller that did not say which reader it meant is refused rather than served
// by whichever came first.
func (s *Supervisor) readerFor(device string) (string, *deviceReader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if device != "" {
		reader, ok := s.readers[device]
		if !ok {
			return "", nil, fmt.Errorf("nfc: no reader %s", device)
		}
		return device, reader, nil
	}

	switch len(s.readers) {
	case 0:
		return "", nil, fmt.Errorf("nfc: no reader is connected")
	case 1:
		for name, reader := range s.readers {
			return name, reader, nil
		}
	}

	names := make([]string, 0, len(s.readers))
	for name := range s.readers {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", nil, fmt.Errorf("nfc: name the reader the operation is for, one of %v", names)
}

// readerNames is every reader, in a settled order so a question asked twice is
// answered the same way.
func (s *Supervisor) readerNames() []string {
	s.mu.Lock()
	names := make([]string, 0, len(s.readers))
	for name := range s.readers {
		names = append(names, name)
	}
	s.mu.Unlock()

	sort.Strings(names)
	return names
}

// readerList is every reader, for applying policy. The caller holds the lock.
func (s *Supervisor) readerList() []*deviceReader {
	out := make([]*deviceReader, 0, len(s.readers))
	for _, reader := range s.readers {
		out = append(out, reader)
	}
	return out
}
