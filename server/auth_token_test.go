package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeVerifier accepts one token.
type fakeVerifier struct {
	valid    string
	deviceID string
}

func (v fakeVerifier) VerifyToken(token string) (string, bool) {
	if token != "" && token == v.valid {
		return v.deviceID, true
	}
	return "", false
}

func authReq(t *testing.T, target, remoteAddr string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.RemoteAddr = remoteAddr
	return httptest.NewRecorder(), r
}

func TestCheckAuthAcceptsDeviceToken(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=device-token", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", Verifier: verifier}); !ok {
		t.Error("a paired device's token was rejected")
	}

	// Bearer is equivalent to the query parameter.
	w, r = authReq(t, "/ws", "192.168.1.20:5000")
	r.Header.Set("Authorization", "Bearer device-token")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", Verifier: verifier}); !ok {
		t.Error("a token presented as a Bearer header was rejected")
	}
}

// A revoked device is one whose token no longer verifies, and it must not fall
// through to the shared secret and get in anyway.
func TestCheckAuthRejectsRevokedToken(t *testing.T) {
	verifier := fakeVerifier{valid: "still-paired", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=revoked-token", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", Verifier: verifier}); ok {
		t.Error("a revoked token was accepted")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// The shared secret keeps working, so upgrading does not strand devices that
// were configured with it.
func TestCheckAuthStillAcceptsSharedSecret(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=shared-secret", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", Verifier: verifier}); !ok {
		t.Error("the shared secret was rejected")
	}
}

// A token works even where no shared secret is configured, so an agent can run
// on per-device credentials alone.
func TestCheckAuthTokenWithoutSharedSecret(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=device-token", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Verifier: verifier}); !ok {
		t.Error("a token was rejected when no shared secret is set")
	}
}

// Loopback is admitted without a credential only where AllowLoopback is set.
func TestCheckAuthLoopbackBypassIsOptIn(t *testing.T) {
	w, r := authReq(t, "/ws", "127.0.0.1:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret"}); ok {
		t.Error("loopback was admitted with no credential and no bypass")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	w, r = authReq(t, "/ws", "127.0.0.1:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", AllowLoopback: true}); !ok {
		t.Error("the bypass did not admit a loopback request")
	}

	// The bypass covers loopback only.
	w, r = authReq(t, "/ws", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", AllowLoopback: true}); ok {
		t.Error("the bypass admitted an off-host request")
	}
}

func TestCheckAuthRejectsNothing(t *testing.T) {
	w, r := authReq(t, "/ws", "192.168.1.20:5000")
	if _, ok := CheckAuth(w, r, AuthOptions{Secret: "shared-secret", Verifier: fakeVerifier{valid: "device-token"}}); ok {
		t.Error("a request with no credential was accepted")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// CheckAPISecret is the pre-token entry point and must behave as it always did.
func TestCheckAPISecretUnchanged(t *testing.T) {
	w, r := authReq(t, "/ws?secret=shared-secret", "192.168.1.20:5000")
	if !CheckAPISecret(w, r, "shared-secret") {
		t.Error("the shared secret was rejected")
	}

	w, r = authReq(t, "/ws?secret=wrong", "192.168.1.20:5000")
	if CheckAPISecret(w, r, "shared-secret") {
		t.Error("a wrong secret was accepted")
	}
}

func TestCheckPairedDeviceAdmitsOnlyTokens(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=device-token", "192.168.1.20:5000")
	if _, ok := CheckPairedDevice(w, r, verifier); !ok {
		t.Error("a paired device was rejected")
	}
}

// Strict mode withdraws the shared secret: holding it is no longer enough.
func TestCheckPairedDeviceRejectsSharedSecret(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws?secret=shared-secret", "192.168.1.20:5000")
	if _, ok := CheckPairedDevice(w, r, verifier); ok {
		t.Error("the shared secret still admitted a device under strict mode")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// And it withdraws the loopback bypass, which is the wider of the two doors.
func TestCheckPairedDeviceRejectsLoopback(t *testing.T) {
	verifier := fakeVerifier{valid: "device-token", deviceID: "dev-1"}

	w, r := authReq(t, "/ws", "127.0.0.1:5000")
	if _, ok := CheckPairedDevice(w, r, verifier); ok {
		t.Error("loopback bypassed the paired-device requirement")
	}
}

// With nothing able to verify a token, strict mode fails closed.
func TestCheckPairedDeviceFailsClosedWithoutVerifier(t *testing.T) {
	w, r := authReq(t, "/ws?secret=anything", "127.0.0.1:5000")
	if _, ok := CheckPairedDevice(w, r, nil); ok {
		t.Error("strict mode admitted a request with no way to verify it")
	}
}
