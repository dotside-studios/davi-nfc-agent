package virtualnfc

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// fakeRoute records the calls a RoutedTag routes to it and reports fixed
// capability bounds.
type fakeRoute struct {
	write, lock, transceive bool // capability bounds

	writes     []writeCall
	locks      []string
	transceves []transceiveCall
	resp       []byte
	err        error
}

type writeCall struct {
	uid  string
	ndef []byte
	lock bool
}
type transceiveCall struct {
	uid  string
	data []byte
	raw  bool
}

func (r *fakeRoute) CanWrite() bool      { return r.write }
func (r *fakeRoute) CanLock() bool       { return r.lock }
func (r *fakeRoute) CanTransceive() bool { return r.transceive }

func (r *fakeRoute) Write(uid string, ndef []byte, lock bool) error {
	r.writes = append(r.writes, writeCall{uid, ndef, lock})
	return r.err
}
func (r *fakeRoute) Lock(uid string) error {
	r.locks = append(r.locks, uid)
	return r.err
}
func (r *fakeRoute) Transceive(uid string, data []byte, raw bool) ([]byte, error) {
	r.transceves = append(r.transceves, transceiveCall{uid, data, raw})
	return r.resp, r.err
}

func fullRoute() *fakeRoute { return &fakeRoute{write: true, lock: true, transceive: true} }

func TestRoutedTagRoutesAllowedOps(t *testing.T) {
	r := fullRoute()
	r.resp = []byte{0x90, 0x00}
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:AA", Type: "NTAG215", Technology: "ISO14443A", Route: r})

	if err := tag.WriteData([]byte{0x01, 0x02}); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if len(r.writes) != 1 || r.writes[0].uid != "04:AA" || r.writes[0].lock {
		t.Fatalf("route.Write calls = %+v, want one unlocked write for 04:AA", r.writes)
	}

	if err := tag.WriteDataAndLock([]byte{0x03}); err != nil {
		t.Fatalf("WriteDataAndLock: %v", err)
	}
	if len(r.writes) != 2 || !r.writes[1].lock {
		t.Fatalf("route.Write calls = %+v, want a second locked write", r.writes)
	}

	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}
	if len(r.locks) != 1 {
		t.Fatalf("route.Lock calls = %v, want one", r.locks)
	}

	resp, err := tag.Transceive([]byte{0x30, 0x00})
	if err != nil {
		t.Fatalf("Transceive: %v", err)
	}
	if len(r.transceves) != 1 || string(resp) != string(r.resp) {
		t.Fatalf("Transceive routed=%+v resp=%x", r.transceves, resp)
	}
}

func TestRoutedTagRefusesWhatRouteCannotCarry(t *testing.T) {
	r := &fakeRoute{} // carries nothing
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:BB", Route: r})

	if err := tag.WriteData([]byte{0x01}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData err = %v, want not-supported", err)
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly err = %v, want not-supported", err)
	}
	if _, err := tag.Transceive([]byte{0x01}); !nfc.IsNotSupportedError(err) {
		t.Errorf("Transceive err = %v, want not-supported", err)
	}
	if len(r.writes)+len(r.locks)+len(r.transceves) != 0 {
		t.Errorf("route was called for a refused op: %+v", r)
	}
}

func TestRoutedTagNilRouteIsReadOnly(t *testing.T) {
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:CC", Snapshot: []byte{0x01}})
	caps := tag.Capabilities()
	if caps.CanWrite || caps.CanLock || caps.CanTransceive {
		t.Errorf("nil-route caps = %+v, want no write/lock/transceive", caps)
	}
	if !caps.CanRead || !caps.ReadsAreSnapshot {
		t.Errorf("caps = %+v, want CanRead and ReadsAreSnapshot", caps)
	}
	if err := tag.WriteData([]byte{0x01}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData err = %v, want not-supported", err)
	}
}

// A tag that declared it cannot be written is refused even by a fully capable
// route; a tag that declared nothing defers to the route.
func TestRoutedTagThreeValuedDeclaration(t *testing.T) {
	deniesWrite := &nfc.TagCapabilities{CanWrite: false, CanLock: true, CanTransceive: true}
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:DD", Declared: deniesWrite, Route: fullRoute()})
	if got, _ := tag.IsWritable(); got {
		t.Error("declared CanWrite=false but IsWritable() = true")
	}
	if caps := tag.Capabilities(); caps.CanWrite {
		t.Error("declared CanWrite=false but Capabilities().CanWrite = true")
	}

	undeclared := NewRoutedTag(RoutedTagConfig{UID: "04:EE", Route: fullRoute()})
	if got, _ := undeclared.IsWritable(); !got {
		t.Error("undeclared tag over a writable route: IsWritable() = false, want true")
	}
}

func TestRoutedTagReadOnlyRefusesWriteAndLock(t *testing.T) {
	ro := &nfc.TagCapabilities{IsReadOnly: true, CanWrite: true, CanLock: true}
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:FF", Declared: ro, Route: fullRoute()})

	caps := tag.Capabilities()
	if caps.CanWrite || caps.CanLock {
		t.Errorf("read-only caps = %+v, want no write/lock", caps)
	}
	if err := tag.WriteData([]byte{0x01}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData err = %v, want not-supported", err)
	}
}

func TestRoutedTagPropagatesRouteError(t *testing.T) {
	sentinel := errors.New("boom")
	r := fullRoute()
	r.err = sentinel
	tag := NewRoutedTag(RoutedTagConfig{UID: "04:11", Route: r})
	if err := tag.WriteData([]byte{0x01}); !errors.Is(err, sentinel) {
		t.Errorf("WriteData err = %v, want %v", err, sentinel)
	}
}

// The RoutedTag must keep the contract every nfc.Tag makes: its advertised
// capabilities agree with its query methods.
func TestRoutedTagCapabilitiesConsistent(t *testing.T) {
	for _, tag := range []*RoutedTag{
		NewRoutedTag(RoutedTagConfig{UID: "04:1", Route: fullRoute()}),
		NewRoutedTag(RoutedTagConfig{UID: "04:2"}),
		NewRoutedTag(RoutedTagConfig{UID: "04:3", Declared: &nfc.TagCapabilities{IsReadOnly: true}, Route: fullRoute()}),
	} {
		if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
			t.Errorf("tag %s: %v", tag.UID(), err)
		}
	}
}
