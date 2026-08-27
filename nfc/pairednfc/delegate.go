package pairednfc

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// errNoHolder is what an operation on a held tag reports when nothing beneath
// this manager holds tags at all, which is a build whose devices are every one
// of them polled through a reader.
var errNoHolder = errors.New("pairednfc: no device beneath this manager holds tags")

// The manager half is delegation, all of it.
//
// It filters nothing. A backend whose devices dial in registers one only after
// it has been admitted, so with RequirePaired on, a device holding no
// credential never registers and never appears in a listing: filtering here
// would be guarding a door the endpoint already locked.
//
// What the position is for is the rest: this is the manager the agent holds, so
// a build cannot have a manager tree without the policy that admits devices
// into it; it is where the revocation subscription lives; and it is where a
// backend the agent dials *out* to would be gated, since such a backend
// enumerates its devices from configuration and passes through no endpoint at
// all. None exists yet.

// OpenDevice opens a device on the child.
func (m *Manager) OpenDevice(deviceStr string) (nfc.Device, error) {
	return m.child.OpenDevice(deviceStr)
}

// Devices lists what the child offers, unchanged.
func (m *Manager) Devices() ([]nfc.DeviceListing, error) {
	return m.child.Devices()
}

// Scans carries the tags the child's devices report.
//
// It is a signal of this manager's own, with the child's connected to it in
// New, rather than the child's handed back. Handing back the child's means
// returning nil for a child that reports no scans, and [nfc.OnScan] would find
// this type satisfying [nfc.TagReporter] and call Connect on that nil.
func (m *Manager) Scans() *event.Signal[nfc.ScannedTag] { return &m.scans }

// DeviceChanges signals when the child's devices are added or removed.
func (m *Manager) DeviceChanges() <-chan struct{} {
	notifier, ok := m.child.(nfc.DeviceChangeNotifier)
	if !ok {
		return nil
	}
	return notifier.DeviceChanges()
}

// The tag router's questions, forwarded whole.
//
// [nfc.TagHolder] is satisfied all-or-nothing: implementing part of it leaves
// [nfc.TagsHeldBy] reporting that nothing here holds a tag, and every operation
// on a tag a device reported fails as though the tag were gone. The child
// answers, or nothing does.

func (m *Manager) holder() nfc.TagHolder { return nfc.TagsHeldBy(m.child) }

// TagOn reports the tag a device is holding.
func (m *Manager) TagOn(deviceID string) (string, string, bool) {
	holder := m.holder()
	if holder == nil {
		return "", "", false
	}
	return holder.TagOn(deviceID)
}

// DevicesHoldingTags lists the devices currently holding one.
func (m *Manager) DevicesHoldingTags() []string {
	holder := m.holder()
	if holder == nil {
		return nil
	}
	return holder.DevicesHoldingTags()
}

// WriteTag encodes msg onto the tag the named device is holding.
func (m *Manager) WriteTag(deviceID, tagUID string, msg *nfc.NDEFMessage, lock bool, idempotencyKey string) (*nfc.WriteResult, error) {
	holder := m.holder()
	if holder == nil {
		return nil, errNoHolder
	}
	return holder.WriteTag(deviceID, tagUID, msg, lock, idempotencyKey)
}

// LockTag makes the tag the named device is holding permanently read-only.
func (m *Manager) LockTag(deviceID, tagUID, idempotencyKey string) (*nfc.LockResult, error) {
	holder := m.holder()
	if holder == nil {
		return nil, errNoHolder
	}
	return holder.LockTag(deviceID, tagUID, idempotencyKey)
}

// TransceiveTag exchanges raw bytes with the tag.
func (m *Manager) TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	holder := m.holder()
	if holder == nil {
		return nil, errNoHolder
	}
	return holder.TransceiveTag(deviceID, tagUID, data, raw)
}

// TagCapabilities reports what the tag the named device is holding supports.
func (m *Manager) TagCapabilities(deviceID, tagUID string) (*nfc.TagCapabilities, error) {
	holder := m.holder()
	if holder == nil {
		return nil, errNoHolder
	}
	return holder.TagCapabilities(deviceID, tagUID)
}

// DisconnectDevice ends the session the child holds for deviceID.
func (m *Manager) DisconnectDevice(deviceID, reason string) bool {
	return nfc.Disconnect(m.child, deviceID, reason)
}

// The full router contract, so nothing here can drift back to a partial one.
var _ nfc.TagHolder = (*Manager)(nil)
