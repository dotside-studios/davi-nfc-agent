package pairednfc_test

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pairednfc"
	"github.com/dotside-studios/davi-nfc-agent/pairing"
)

// reader is a locally attached backend: it has no endpoint, no identity, and no
// session to end. Nothing about a credential applies to it, and this manager
// must leave it exactly as it found it.
type reader struct{}

func (reader) OpenDevice(string) (nfc.Device, error) { return nil, errNotOpened }

func (reader) Devices() ([]nfc.DeviceListing, error) {
	return []nfc.DeviceListing{{
		Path:         "ACR122U 00 00",
		ID:           "ACR122U 00 00",
		Capabilities: nfc.DeviceCapabilities{CanPoll: true},
	}}, nil
}

var errNotOpened = errors.New("reader opens nothing in a test")

// phone is a backend whose devices dial in: it reports scans, holds tags, and
// can end a session by identity.
type phone struct {
	scans        event.Signal[nfc.ScannedTag]
	changes      chan struct{}
	disconnected []string
	holding      string
	closed       bool
}

func newPhone() *phone {
	return &phone{changes: make(chan struct{}, 1), holding: "phone-9f2a"}
}

func (p *phone) OpenDevice(string) (nfc.Device, error) { return nil, errNotOpened }

func (p *phone) Devices() ([]nfc.DeviceListing, error) {
	return []nfc.DeviceListing{{
		Path:         p.holding,
		ID:           p.holding,
		Capabilities: nfc.DeviceCapabilities{SupportsEvents: true},
	}}, nil
}

func (p *phone) Scans() *event.Signal[nfc.ScannedTag] { return &p.scans }
func (p *phone) DeviceChanges() <-chan struct{}       { return p.changes }
func (p *phone) Close()                               { p.closed = true }

func (p *phone) DisconnectDevice(deviceID, _ string) bool {
	p.disconnected = append(p.disconnected, deviceID)
	return deviceID == p.holding
}

func (p *phone) TagOn(deviceID string) (string, string, bool) {
	if deviceID == "" || deviceID == p.holding {
		return p.holding, "04:A2:B3:C4", true
	}
	return "", "", false
}

func (p *phone) DevicesHoldingTags() []string { return []string{p.holding} }

func (p *phone) WriteTag(string, string, *nfc.NDEFMessage, bool, string) (*nfc.WriteResult, error) {
	return &nfc.WriteResult{}, nil
}
func (p *phone) LockTag(string, string, string) (*nfc.LockResult, error) {
	return &nfc.LockResult{}, nil
}
func (p *phone) TransceiveTag(string, string, []byte, bool) ([]byte, error) { return []byte{0x90}, nil }
func (p *phone) TagCapabilities(string, string) (*nfc.TagCapabilities, error) {
	return &nfc.TagCapabilities{}, nil
}

func over(t *testing.T, child nfc.Manager) *pairednfc.Manager {
	t.Helper()

	m, err := pairednfc.New(child, pairednfc.Options{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// A reader attached to this machine is beneath this manager and untouched by it,
// even with paired devices required. There is no endpoint in front of it and no
// identity to check, so gating it would take the readers away from a build that
// simply turned the requirement on.
func TestALocalReaderIsUnaffected(t *testing.T) {
	m := over(t, reader{})
	m.Require(func() bool { return true })

	listings, err := m.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(listings) != 1 || listings[0].Path != "ACR122U 00 00" {
		t.Fatalf("Devices() = %v, want the reader listed unchanged", listings)
	}

	// It reaches the child rather than being refused before it gets there.
	if _, err := m.OpenDevice("ACR122U 00 00"); !errors.Is(err, errNotOpened) {
		t.Errorf("OpenDevice error = %v, want the child's", err)
	}
}

// A revoked credential names a device on a backend that holds sessions. A
// backend that holds none is asked nothing and reports nothing.
func TestRevokingReachesOnlyABackendThatHoldsSessions(t *testing.T) {
	p := newPhone()
	m := over(t, p)

	registry, ok := m.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", m.PairedDevices())
	}
	device, _, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if err := registry.Revoke(device.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(p.disconnected) != 1 || p.disconnected[0] != device.ID {
		t.Errorf("the backend was asked to disconnect %v, want just %q", p.disconnected, device.ID)
	}

	// A backend with no sessions is simply not a disconnector, and asking it is
	// not an error.
	if over(t, reader{}).DisconnectDevice("anyone", "revoked") {
		t.Error("a backend holding no sessions reported ending one")
	}
}

// Scans pass through. They are not filtered: a scan only exists because the
// endpoint already admitted the device that reported it.
func TestScansReachSubscribersThroughTheManager(t *testing.T) {
	p := newPhone()
	m := over(t, p)

	var got []nfc.ScannedTag
	if conn := nfc.OnScan(m, func(s nfc.ScannedTag) { got = append(got, s) }); conn == nil {
		t.Fatal("OnScan found nothing to subscribe to")
	}

	p.scans.Emit(nfc.ScannedTag{Device: "phone-9f2a"})

	if len(got) != 1 || got[0].Device != "phone-9f2a" {
		t.Errorf("subscriber saw %v, want the scan the backend reported", got)
	}
}

// A child that reports no scans must still leave OnScan safe. This manager
// always satisfies TagReporter, so handing back the child's nil signal would
// have OnScan call Connect on it.
func TestOnScanIsSafeOverABackendThatReportsNone(t *testing.T) {
	m := over(t, reader{})

	if conn := nfc.OnScan(m, func(nfc.ScannedTag) {}); conn == nil {
		t.Fatal("OnScan reported nothing to subscribe to")
	}
}

// nfc.TagHolder is satisfied all-or-nothing. Implementing part of it leaves
// TagsHeldBy reporting that nothing holds a tag, and every operation on a tag a
// device reported fails as though the tag were gone — which is exactly what a
// partial forward here did.
func TestTheWholeTagRouterContractIsForwarded(t *testing.T) {
	m := over(t, newPhone())

	holder := nfc.TagsHeldBy(m)
	if holder == nil {
		t.Fatal("TagsHeldBy found no holder behind the manager")
	}

	device, uid, ok := holder.TagOn("phone-9f2a")
	if !ok || device != "phone-9f2a" || uid != "04:A2:B3:C4" {
		t.Errorf("TagOn = %q, %q, %v; want the phone and its tag", device, uid, ok)
	}
	if got := holder.DevicesHoldingTags(); len(got) != 1 {
		t.Errorf("DevicesHoldingTags() = %v, want the one phone", got)
	}
	if _, err := holder.WriteTag("phone-9f2a", uid, nil, false, "key"); err != nil {
		t.Errorf("WriteTag: %v", err)
	}
	if _, err := holder.LockTag("phone-9f2a", uid, "key"); err != nil {
		t.Errorf("LockTag: %v", err)
	}
	if _, err := holder.TransceiveTag("phone-9f2a", uid, []byte{0x00}, true); err != nil {
		t.Errorf("TransceiveTag: %v", err)
	}
	if _, err := holder.TagCapabilities("phone-9f2a", uid); err != nil {
		t.Errorf("TagCapabilities: %v", err)
	}
}

// A backend holding no tags answers the router with an error rather than a
// panic, and TagsHeldBy still finds this manager, since it cannot implement the
// interface conditionally.
func TestTheRouterOverABackendHoldingNoTags(t *testing.T) {
	m := over(t, reader{})

	if _, _, ok := m.TagOn("ACR122U 00 00"); ok {
		t.Error("a reader-only backend reported holding a tag")
	}
	if _, err := m.TransceiveTag("ACR122U 00 00", "uid", nil, true); err == nil {
		t.Error("an operation on a tag nothing holds reported success")
	}
}

func TestDeviceChangesAndCloseReachTheChild(t *testing.T) {
	p := newPhone()
	m := over(t, p)

	changes := m.DeviceChanges()
	if changes == nil {
		t.Fatal("DeviceChanges reported no channel")
	}
	p.changes <- struct{}{}
	select {
	case <-changes:
	default:
		t.Error("a device change did not come through the manager")
	}

	m.Close()
	if !p.closed {
		t.Error("Close did not reach the child")
	}
}
