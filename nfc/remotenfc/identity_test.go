package remotenfc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/deviceid"
)

// admitAs mounts the endpoint behind something that names every connection id,
// the way a credential check would.
func admitAs(t *testing.T, m *Manager, id string) string {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Handler(ServerOptions{}).ServeHTTP(w, deviceid.With(r, id))
	}))
	t.Cleanup(func() {
		ts.Close()
		m.Close()
	})

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device"
}

// The identity a connection was admitted under is the identity the device
// registers with. Without it a paired device would get a fresh ID on every
// connection, nothing could match its session to the credential it holds, and
// revoking it would end no session.
func TestADeviceRegistersUnderTheIdentityItWasAdmittedWith(t *testing.T) {
	m := NewManager(time.Minute)

	_, deviceID := connectDevice(t, admitAs(t, m, "paired-device-7"))

	if deviceID != "paired-device-7" {
		t.Fatalf("device registered as %q, want %q", deviceID, "paired-device-7")
	}
}

// Nothing admitted this connection, so the driver names it itself. An endpoint
// mounted with no credential check in front of it serves every device.
func TestAnUnadmittedDeviceRegistersUnderAMintedIdentity(t *testing.T) {
	m, url := serveManager(t, time.Minute)

	_, deviceID := connectDevice(t, url)

	if deviceID == "" {
		t.Fatal("an unadmitted device registered under no identity")
	}
	if _, ok := m.GetDevice(deviceID); !ok {
		t.Fatalf("the manager does not hold the device it registered as %q", deviceID)
	}
}

// The minted identity is per connection, so two unadmitted devices must not
// collide into one registration.
func TestUnadmittedDevicesGetDistinctIdentities(t *testing.T) {
	m, url := serveManager(t, time.Minute)

	_, first := connectDevice(t, url)
	_, second := connectDevice(t, url)

	if first == second {
		t.Fatalf("both devices registered as %q", first)
	}
	awaitDeviceCount(t, m, "after two registrations", 2)
}
