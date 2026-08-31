package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// nextOK records that the wrapped handler ran, and writes a 200.
func nextOK(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

// corsReq builds a request to host carrying origin, or none when origin is "".
func corsReq(method, host, origin string) *http.Request {
	r := httptest.NewRequest(method, "http://"+host+"/state", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestCORSPolicyEchoesAnAllowedOrigin(t *testing.T) {
	policy := &fakePolicy{allow: map[string]bool{"console.example": true}}
	ran := false
	h := CORSPolicy(policy, nextOK(&ran))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodGet, "kiosk:9470", "https://console.example"))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the echoed origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Access-Control-Allow-Methods is unset for an allowed origin")
	}
	if !ran {
		t.Error("the wrapped handler did not run")
	}
}

// A refused origin gets no allow header — the browser then blocks the read —
// and is recorded through the policy, but the handler still runs.
func TestCORSPolicyRefusesAndRecordsAForeignOrigin(t *testing.T) {
	policy := &fakePolicy{allow: map[string]bool{}}
	ran := false
	h := CORSPolicy(policy, nextOK(&ran))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodGet, "kiosk:9470", "https://evil.example"))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none for a refused origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin even when refused", got)
	}
	if len(policy.blocked) != 1 || policy.blocked[0] != "evil.example" {
		t.Errorf("blocked = %v, want [evil.example]", policy.blocked)
	}
	if !ran {
		t.Error("the wrapped handler must still run; the header is the enforcement")
	}
}

func TestCORSPolicyGrantsSameHostWithoutRecording(t *testing.T) {
	policy := &fakePolicy{allow: map[string]bool{}}
	h := CORSPolicy(policy, nextOK(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodGet, "kiosk:9470", "http://kiosk:9470"))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://kiosk:9470" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the same-host origin echoed", got)
	}
	if len(policy.blocked) != 0 {
		t.Errorf("a same-host origin was recorded blocked: %v", policy.blocked)
	}
}

func TestCORSPolicyNoOriginAddsNoHeaders(t *testing.T) {
	ran := false
	h := CORSPolicy(&fakePolicy{}, nextOK(&ran))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodGet, "kiosk:9470", ""))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none without an Origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want none without an Origin", got)
	}
	if !ran {
		t.Error("the wrapped handler did not run for a same-origin request")
	}
}

func TestCORSPolicyAnswersPreflight(t *testing.T) {
	policy := &fakePolicy{allow: map[string]bool{"console.example": true}}

	// Allowed preflight: 200 with the echo, and the handler does not run.
	ran := false
	h := CORSPolicy(policy, nextOK(&ran))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodOptions, "kiosk:9470", "https://console.example"))
	if rec.Code != http.StatusOK {
		t.Errorf("preflight status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://console.example" {
		t.Error("an allowed preflight was not granted")
	}
	if ran {
		t.Error("the wrapped handler ran for a preflight")
	}

	// Refused preflight: still 200, but no allow header, so the browser fails it.
	h = CORSPolicy(policy, nextOK(new(bool)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, corsReq(http.MethodOptions, "kiosk:9470", "https://evil.example"))
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("a refused preflight carried an allow header")
	}
}

func TestCORSPolicyNilGrantsOnlySameHost(t *testing.T) {
	h := CORSPolicy(nil, nextOK(new(bool)))

	same := httptest.NewRecorder()
	h.ServeHTTP(same, corsReq(http.MethodGet, "kiosk:9470", "http://kiosk:9470"))
	if same.Header().Get("Access-Control-Allow-Origin") != "http://kiosk:9470" {
		t.Error("nil policy refused a same-host origin")
	}

	foreign := httptest.NewRecorder()
	h.ServeHTTP(foreign, corsReq(http.MethodGet, "kiosk:9470", "https://console.example"))
	if foreign.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("nil policy echoed a foreign origin")
	}
}
