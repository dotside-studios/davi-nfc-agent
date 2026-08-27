package pairednfc_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pairednfc"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceid"
)

// admitted records what reached the wrapped endpoint, which is what a backend
// would register the device under.
type admitted struct {
	reached  bool
	identity string
}

// gate builds a manager over a mock backend and wraps an endpoint that records
// the identity it was reached with.
func gate(t *testing.T, policy pairednfc.Policy) (*pairednfc.Manager, *admitted, http.Handler) {
	t.Helper()

	m, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{
		ConfigDir: t.TempDir(),
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seen := &admitted{}
	endpoint := m.Admit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.reached = true
		seen.identity = deviceid.Of(r)
	}))

	return m, seen, endpoint
}

// pairOne issues a credential and returns the token a device would present.
func pairOne(t *testing.T, m *pairednfc.Manager) (string, string) {
	t.Helper()

	registry, ok := m.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry to pair on", m.PairedDevices())
	}
	device, token, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	return device.ID, token
}

// reach presents a credential from off this machine and reports the status.
// Off this machine deliberately: a loopback request bypasses the shared secret,
// so a test driving one through 127.0.0.1 would pass whatever the check did.
func reach(t *testing.T, endpoint http.Handler, secret string) int {
	t.Helper()
	return call(endpoint, secret, "192.0.2.7:34512")
}

// reachFromLoopback presents a credential from this machine, where the bypass
// applies.
func reachFromLoopback(t *testing.T, endpoint http.Handler, secret string) int {
	t.Helper()
	return call(endpoint, secret, "127.0.0.1:34512")
}

func call(endpoint http.Handler, secret, remoteAddr string) int {
	url := "/ws?mode=device"
	if secret != "" {
		url += "&secret=" + secret
	}
	r := httptest.NewRequest(http.MethodGet, url, nil)
	r.RemoteAddr = remoteAddr

	rec := httptest.NewRecorder()
	endpoint.ServeHTTP(rec, r)
	return rec.Code
}

// A paired device is admitted under the identity it paired with, and that
// identity reaches the backend. Without it a paired device would register under
// a fresh ID every connection and revoking it would end nothing.
func TestAPairedTokenIsAdmittedUnderItsOwnIdentity(t *testing.T) {
	m, seen, endpoint := gate(t, pairednfc.Policy{Secret: func() string { return "shared" }})
	id, token := pairOne(t, m)

	if status := reach(t, endpoint, token); status != http.StatusOK {
		t.Fatalf("a paired device was refused (status %d)", status)
	}
	if !seen.reached {
		t.Fatal("the endpoint behind the check was never reached")
	}
	if seen.identity != id {
		t.Errorf("the endpoint saw identity %q, want %q", seen.identity, id)
	}
}

// The shared secret admits but names nobody: there is no device to revoke
// behind it.
func TestTheSharedSecretAdmitsWithoutNamingADevice(t *testing.T) {
	_, seen, endpoint := gate(t, pairednfc.Policy{Secret: func() string { return "shared" }})

	if status := reach(t, endpoint, "shared"); status != http.StatusOK {
		t.Fatalf("the shared secret was refused (status %d)", status)
	}
	if seen.identity != "" {
		t.Errorf("the shared secret named device %q; it identifies nobody", seen.identity)
	}
}

func TestAWrongSecretIsRefused(t *testing.T) {
	_, seen, endpoint := gate(t, pairednfc.Policy{Secret: func() string { return "shared" }})

	if status := reach(t, endpoint, "wrong"); status != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", status, http.StatusUnauthorized)
	}
	if seen.reached {
		t.Error("a refused connection reached the endpoint behind the check")
	}
}

// The secret is read per connection. It used to be captured, leaving the
// endpoint admitting a secret the console had already rotated away.
func TestTheSecretIsReadPerConnection(t *testing.T) {
	secret := "old"
	_, _, endpoint := gate(t, pairednfc.Policy{Secret: func() string { return secret }})

	if status := reach(t, endpoint, "old"); status != http.StatusOK {
		t.Fatalf("the current secret was refused (status %d)", status)
	}

	secret = "fresh"

	if status := reach(t, endpoint, "old"); status != http.StatusUnauthorized {
		t.Errorf("the rotated-away secret is still admitted (status %d)", status)
	}
	if status := reach(t, endpoint, "fresh"); status != http.StatusOK {
		t.Errorf("the fresh secret was refused (status %d)", status)
	}
}

// RequirePaired drops the shared secret as well as the bypass: only a credential
// issued at pairing admits.
func TestRequirePairedDropsTheSharedSecret(t *testing.T) {
	m, _, endpoint := gate(t, pairednfc.Policy{
		Secret:        func() string { return "shared" },
		RequirePaired: func() bool { return true },
	})
	_, token := pairOne(t, m)

	if status := reach(t, endpoint, "shared"); status != http.StatusUnauthorized {
		t.Errorf("the shared secret admitted a device while paired ones were required (status %d)", status)
	}
	if status := reach(t, endpoint, token); status != http.StatusOK {
		t.Errorf("a paired device was refused (status %d)", status)
	}
}

// Revoking the last device must not fall open. An empty registry under
// RequirePaired admits nobody.
func TestRequirePairedWithNoPairedDevicesAdmitsNobody(t *testing.T) {
	m, _, endpoint := gate(t, pairednfc.Policy{
		Secret:        func() string { return "shared" },
		RequirePaired: func() bool { return true },
	})
	_, token := pairOne(t, m)

	if err := m.PairedDevices().RevokeAll(); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	if status := reach(t, endpoint, token); status != http.StatusUnauthorized {
		t.Errorf("a revoked credential was admitted (status %d)", status)
	}
	if status := reach(t, endpoint, "shared"); status != http.StatusUnauthorized {
		t.Errorf("revoking the last device fell back to the shared secret (status %d)", status)
	}
}

// The kiosk front end runs on this machine and has never had to know the
// secret. The bypass survives, and names nobody.
func TestALoopbackRequestBypassesTheSharedSecret(t *testing.T) {
	_, seen, endpoint := gate(t, pairednfc.Policy{Secret: func() string { return "shared" }})

	if status := reachFromLoopback(t, endpoint, ""); status != http.StatusOK {
		t.Fatalf("a request from this machine was refused (status %d)", status)
	}
	if seen.identity != "" {
		t.Errorf("the bypass named device %q; it identifies nobody", seen.identity)
	}
}

// RequirePaired drops the bypass too.
func TestRequirePairedDropsTheLoopbackBypass(t *testing.T) {
	_, _, endpoint := gate(t, pairednfc.Policy{
		Secret:        func() string { return "shared" },
		RequirePaired: func() bool { return true },
	})

	if status := reachFromLoopback(t, endpoint, ""); status != http.StatusUnauthorized {
		t.Errorf("the loopback bypass admitted a device while paired ones were required (status %d)", status)
	}
}

// A build with no secret and no paired devices admits everyone, which is what
// an agent reached only over a trusted transport wants.
func TestWithNoCredentialsAtAllEveryDeviceIsAdmitted(t *testing.T) {
	_, seen, endpoint := gate(t, pairednfc.Policy{})

	if status := reach(t, endpoint, ""); status != http.StatusOK {
		t.Fatalf("a device was refused by a policy holding no credentials (status %d)", status)
	}
	if !seen.reached {
		t.Error("the endpoint behind the check was never reached")
	}
}
