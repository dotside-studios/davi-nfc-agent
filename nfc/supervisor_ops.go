package nfc

import (
	"fmt"
	"sort"
	"strings"
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

// TagOn reports the tag a reader is holding, and the reader holding it. An
// empty device asks the only reader there is.
func (s *Supervisor) TagOn(device string) (holder string, tag Tag, ok bool) {
	name, reader, err := s.readerFor(device)
	if err != nil {
		return "", nil, false
	}

	tags, err := reader.GetTags()
	if err != nil || len(tags) != 1 {
		return "", nil, false
	}
	return name, tags[0], true
}

// Holding reports which reader last scanned a tag with this UID. It is a cached
// view and only picks the reader: the reader re-checks the tag it is holding
// when it performs the operation.
func (s *Supervisor) Holding(uid string) (device string, ok bool) {
	if uid == "" {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for name, reader := range s.readers {
		if strings.EqualFold(reader.GetLastScannedData(), uid) {
			return name, true
		}
	}
	return "", false
}

// Present reports a reader with a card on it, for a request that named no tag.
func (s *Supervisor) Present() (device string, ok bool) {
	s.mu.Lock()
	readers := make(map[string]*NFCReader, len(s.readers))
	for name, reader := range s.readers {
		readers[name] = reader
	}
	s.mu.Unlock()

	names := make([]string, 0, len(readers))
	for name := range readers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if readers[name].GetDeviceStatus().CardPresent {
			return name, true
		}
	}
	return "", false
}

// Any names a reader to fall back on when none is holding a card, so a request
// that has to go somewhere reaches one rather than being refused for having no
// route.
func (s *Supervisor) Any() (device string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.readers))
	for name := range s.readers {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
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
func (s *Supervisor) readerFor(device string) (string, *NFCReader, error) {
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

// readerList is every reader, for applying policy. The caller holds the lock.
func (s *Supervisor) readerList() []*NFCReader {
	out := make([]*NFCReader, 0, len(s.readers))
	for _, reader := range s.readers {
		out = append(out, reader)
	}
	return out
}
