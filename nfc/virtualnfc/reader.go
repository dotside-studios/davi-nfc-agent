package virtualnfc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// DefaultReaderDevice is the device a Reader plugs when NewReader is called with
// no names.
const DefaultReaderDevice = "virtual-0"

// TagSpec declares a virtual tag for a Reader to present. Text and URI are
// conveniences for the common single-record cases; Records overrides both for
// arbitrary content. A tag with no content declared presents empty (a blank,
// writable tag).
type TagSpec struct {
	UID        string
	Type       string // display type; default "Virtual"
	Technology string // default "virtual"

	Text    string // single text record, when Records is empty
	URI     string // single URI record, when Records and Text are empty
	Records []nfc.NDEFRecordBuilder

	// ReadOnly presents the tag already locked: writes and locks are refused.
	ReadOnly bool
}

func (s TagSpec) records() []nfc.NDEFRecordBuilder {
	switch {
	case len(s.Records) > 0:
		return s.Records
	case s.Text != "":
		return []nfc.NDEFRecordBuilder{&nfc.NDEFText{Content: s.Text, Language: "en"}}
	case s.URI != "":
		return []nfc.NDEFRecordBuilder{&nfc.NDEFURI{Content: s.URI}}
	default:
		return nil
	}
}

// Reader is a standalone software NFC reader: a reader whose tags come from
// software rather than PC/SC hardware, presented on a real nfc.Supervisor and
// read and written through the production pipeline. It needs no reader plugged
// in, no test harness, and no phone.
//
// It is a polled reader (the Supervisor opens it and holds its tags), so read,
// write, lock and capability queries all work through the embedded Supervisor.
// Tags are route-backed: reads come from the content declared at present time,
// and writes are carried by an in-process store, so a write acknowledges and a
// lock sticks without any silicon.
//
//	r, _ := virtualnfc.NewReader()
//	r.Present("", virtualnfc.TagSpec{UID: "04:A1:B2:C3", Text: "hello"})
//	caps, _ := r.Capabilities("")
//
// The embedded *nfc.Supervisor exposes the production per-device operations
// (WriteMessage, Capabilities, Lock, Transceive, Scans, Status) directly.
type Reader struct {
	*nfc.Supervisor
	mgr   *Manager
	route *memRoute

	mu      sync.Mutex
	devices map[string]*Device
	first   string
}

// NewReader builds a reader with one polled device per name (DefaultReaderDevice
// if none), its Supervisor started. Call Close to stop it.
func NewReader(deviceNames ...string) (*Reader, error) {
	if len(deviceNames) == 0 {
		deviceNames = []string{DefaultReaderDevice}
	}

	mgr := NewManager()
	r := &Reader{mgr: mgr, route: newMemRoute(), devices: make(map[string]*Device)}
	for i, name := range deviceNames {
		dev := NewDevice(name, PollMode, "virtual-reader")
		mgr.Plug(name, dev)
		r.devices[name] = dev
		if i == 0 {
			r.first = name
		}
	}

	sup, err := nfc.NewSupervisor(mgr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if err := sup.Start(); err != nil {
		return nil, err
	}
	r.Supervisor = sup
	return r, nil
}

// resolve names the device to act on: an empty string means the first one.
func (r *Reader) resolve(device string) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if device == "" {
		device = r.first
	}
	dev, ok := r.devices[device]
	if !ok {
		return nil, fmt.Errorf("virtualnfc: no device %q", device)
	}
	return dev, nil
}

// Present taps a tag onto a device (empty device = the first one).
func (r *Reader) Present(device string, spec TagSpec) error {
	if spec.UID == "" {
		return fmt.Errorf("virtualnfc: tag needs a UID")
	}
	dev, err := r.resolve(device)
	if err != nil {
		return err
	}
	card, err := r.makeCard(spec)
	if err != nil {
		return err
	}
	dev.Present(card)
	return nil
}

// Remove takes the tag with the given UID off a device.
func (r *Reader) Remove(device, uid string) error {
	dev, err := r.resolve(device)
	if err != nil {
		return err
	}
	dev.Remove(uid)
	r.route.forget(uid)
	return nil
}

// Devices lists the reader's device names.
func (r *Reader) Devices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.devices))
	for name := range r.devices {
		out = append(out, name)
	}
	return out
}

// Write encodes a message onto the tag on a device, through the production write
// pipeline. It overwrites whatever the tag held.
func (r *Reader) Write(device string, msg *nfc.NDEFMessage) (*nfc.WriteResult, error) {
	return r.Supervisor.WriteMessage(context.Background(), device, msg, nfc.WriteOptions{Overwrite: true, Index: -1})
}

// Capabilities reports what the tag on a device supports.
func (r *Reader) Capabilities(device string) (*nfc.TagCapabilities, error) {
	return r.Supervisor.Capabilities(context.Background(), device, "")
}

// Close stops the reader.
func (r *Reader) Close() { r.Supervisor.Stop() }

func (r *Reader) makeCard(spec TagSpec) (*Card, error) {
	var snapshot []byte
	var msg *nfc.NDEFMessage
	if records := spec.records(); len(records) > 0 {
		m, err := Message(records...)
		if err != nil {
			return nil, fmt.Errorf("virtualnfc: build NDEF for %s: %w", spec.UID, err)
		}
		enc, err := m.Encode()
		if err != nil {
			return nil, fmt.Errorf("virtualnfc: encode NDEF for %s: %w", spec.UID, err)
		}
		snapshot, msg = enc, m
	}

	r.route.seed(spec.UID, snapshot, spec.ReadOnly)

	var declared *nfc.TagCapabilities
	if spec.ReadOnly {
		declared = &nfc.TagCapabilities{IsReadOnly: true}
	}

	typ := spec.Type
	if typ == "" {
		typ = "Virtual"
	}
	tech := spec.Technology
	if tech == "" {
		tech = "virtual"
	}

	return NewRoutedCard(RoutedTagConfig{
		UID:        spec.UID,
		Type:       typ,
		Technology: tech,
		Declared:   declared,
		Snapshot:   snapshot,
		NDEF:       msg,
		Route:      r.route,
	}), nil
}
