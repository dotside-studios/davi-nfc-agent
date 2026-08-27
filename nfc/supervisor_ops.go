package nfc

import (
	"context"
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

// Tags, wherever they are.

// The supervisor answers for every tag the agent can reach: the one on a reader
// it polls, and the one a device holds and reported for itself. What both scan
// already arrives on one signal, so what can be done to either is asked in one
// place too. See [TagHolder].

var _ TagHolder = (*Supervisor)(nil)

// TagOn reports the tag a device is holding, by UID. An empty device asks a
// reader with a card on it, and failing that the most recent scan a device
// reported.
//
// A reader answers from its last scan rather than a fresh read: the answer
// selects the reader, which checks the tag it actually has when it performs the
// operation.
func (s *Supervisor) TagOn(device string) (holder, uid string, ok bool) {
	if device != "" {
		if s.Operates(device) {
			return s.tagOnReader(device)
		}
		return s.tagOnDevice(device)
	}

	// A reader whose tag can be named is preferred over one holding a tag it
	// could not read, which is all an unnamed request has to choose by.
	unnamed := ""
	for _, name := range s.readerNames() {
		holder, uid, ok := s.tagOnReader(name)
		if !ok {
			continue
		}
		if uid != "" {
			return holder, uid, true
		}
		if unnamed == "" {
			unnamed = holder
		}
	}

	if holder, uid, ok := s.tagOnDevice(""); ok {
		return holder, uid, true
	}
	if unnamed != "" {
		return unnamed, "", true
	}
	return "", "", false
}

// tagOnReader is the tag a reader last scanned, or the one it has on it that it
// could not read: a blank or damaged tag is still a tag to write to, and the
// reader checks what it has when the operation runs.
func (s *Supervisor) tagOnReader(device string) (string, string, bool) {
	s.mu.Lock()
	reader, known := s.readers[device]
	s.mu.Unlock()
	if !known {
		return "", "", false
	}

	if uid := reader.GetLastScannedData(); uid != "" {
		return device, uid, true
	}
	if reader.GetDeviceStatus().CardPresent {
		return device, "", true
	}
	return "", "", false
}

// tagOnDevice is the tag one of the manager's own devices reported.
func (s *Supervisor) tagOnDevice(device string) (string, string, bool) {
	if s.devices == nil {
		return "", "", false
	}
	return s.devices.TagOn(device)
}

// DevicesHoldingTags lists what has a tag to name, readers first: an operation
// for a tag on a reader should not wait on a device holding one too.
//
// A reader's last scan stands until it reports another, so a route picked from
// it is confirmed by the reader when the operation runs.
func (s *Supervisor) DevicesHoldingTags() []string {
	var holding []string
	for _, name := range s.readerNames() {
		if _, _, ok := s.tagOnReader(name); ok {
			holding = append(holding, name)
		}
	}
	if s.devices != nil {
		holding = append(holding, s.devices.DevicesHoldingTags()...)
	}
	return holding
}

// heldElsewhere names what performs an operation when the tag is not on a
// reader this opened. Nil means the readers take it.
func (s *Supervisor) heldElsewhere(device string) TagHolder {
	if s.devices == nil {
		return nil
	}
	if device == "" {
		if len(s.readerNames()) > 0 {
			return nil
		}
		return s.devices
	}
	if s.Operates(device) {
		return nil
	}
	return s.devices
}

// WriteTag encodes msg onto the tag the named device is holding. A reader reads
// it back where the tag allows, so the result says whether it was confirmed.
//
// The idempotency key is a device's: a reader is asked once and answers for
// what it did, where a device may have applied the write already.
func (s *Supervisor) WriteTag(ctx context.Context, device, tagUID string, msg *NDEFMessage, lock bool, idempotencyKey string) (*WriteResult, error) {
	if holder := s.heldElsewhere(device); holder != nil {
		return holder.WriteTag(ctx, device, tagUID, msg, lock, idempotencyKey)
	}
	return s.WriteMessage(ctx, device, msg, WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      lock,
		ExpectUID: tagUID,
	})
}

// LockTag makes the tag the named device is holding permanently read-only.
func (s *Supervisor) LockTag(ctx context.Context, device, tagUID, idempotencyKey string) (*LockResult, error) {
	if holder := s.heldElsewhere(device); holder != nil {
		return holder.LockTag(ctx, device, tagUID, idempotencyKey)
	}
	return s.Lock(ctx, device, tagUID)
}

// TransceiveTag exchanges raw bytes with the tag the named device is holding. A
// reader speaks to the tag directly, so raw is what it always is there.
func (s *Supervisor) TransceiveTag(ctx context.Context, device, tagUID string, data []byte, raw bool) ([]byte, error) {
	if holder := s.heldElsewhere(device); holder != nil {
		return holder.TransceiveTag(ctx, device, tagUID, data, raw)
	}
	return s.Transceive(ctx, device, data, tagUID)
}

// TagCapabilities reports what the tag the named device is holding supports.
func (s *Supervisor) TagCapabilities(ctx context.Context, device, tagUID string) (*TagCapabilities, error) {
	if holder := s.heldElsewhere(device); holder != nil {
		return holder.TagCapabilities(ctx, device, tagUID)
	}
	return s.Capabilities(ctx, device, tagUID)
}

// Operates reports whether this is a reader the supervisor opened, as opposed
// to a device that reports its own scans.
func (s *Supervisor) Operates(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.readers[device]
	return ok
}

// Operations. Each names the reader it applies to, so one reader's operation
// does not queue behind another's.

// WriteMessage encodes a message onto the tag on the named reader.
func (s *Supervisor) WriteMessage(ctx context.Context, device string, msg *NDEFMessage, opts WriteOptions) (*WriteResult, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.WriteMessageWithResult(ctx, msg, opts)
}

// Lock makes the tag on the named reader read-only. expectUID refuses a tag
// other than the one the caller named.
func (s *Supervisor) Lock(ctx context.Context, device, expectUID string) (*LockResult, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.LockCardExpecting(ctx, expectUID)
}

// Transceive exchanges raw bytes with the tag on the named reader.
func (s *Supervisor) Transceive(ctx context.Context, device string, data []byte, expectUID string) ([]byte, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.TransceiveExpecting(ctx, data, expectUID)
}

// Capabilities reports what the tag on the named reader supports.
func (s *Supervisor) Capabilities(ctx context.Context, device, expectUID string) (*TagCapabilities, error) {
	_, reader, err := s.readerFor(device)
	if err != nil {
		return nil, err
	}
	return reader.GetCapabilitiesExpecting(ctx, expectUID)
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
