package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeCAReader supplies a static PEM CA + fingerprint for tests so we
// don't need to spin up the real Manager (which calls truststore).
type fakeCAReader struct {
	pem         []byte
	fingerprint string
	readErr     error
	fpErr       error
}

func (f *fakeCAReader) ReadCACert() ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.pem, nil
}

func (f *fakeCAReader) GetCAFingerprint() (string, error) {
	if f.fpErr != nil {
		return "", f.fpErr
	}
	return f.fingerprint, nil
}

// newFakeCAReader generates a real (throwaway) self-signed cert so any
// downstream parsing (DER decode, x509 parse) works.
func newFakeCAReader(t *testing.T) *fakeCAReader {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &fakeCAReader{pem: pemBytes, fingerprint: "AB:CD"}
}

func newTestServer(t *testing.T) (*BootstrapServer, *httptest.Server) {
	t.Helper()
	srv := NewBootstrapServer(newFakeCAReader(t), 0)

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/install", srv.handleInstall)
	mux.HandleFunc("/install/ios", srv.handleAppleProfile)
	mux.HandleFunc("/install/android", srv.handleAndroidCert)
	mux.HandleFunc("/qr.png", srv.handleQR)
	mux.HandleFunc("/ca.pem", srv.handleRawCA)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestGeneratePIN(t *testing.T) {
	for range 100 {
		pin := generatePIN()
		if len(pin) != 6 {
			t.Errorf("expected 6 digits, got %q", pin)
		}
		for _, c := range pin {
			if c < '0' || c > '9' {
				t.Errorf("non-digit in PIN %q", pin)
			}
		}
	}
}

func TestPINMatch(t *testing.T) {
	tests := []struct {
		got, want string
		ok        bool
	}{
		{"123456", "123456", true},
		{"123456", "654321", false},
		{"", "123456", false},
		{"123456", "", false},
		{"1234567", "123456", false},
		{"12345", "123456", false},
	}
	for _, tt := range tests {
		if got := pinMatch(tt.got, tt.want); got != tt.ok {
			t.Errorf("pinMatch(%q, %q) = %v, want %v", tt.got, tt.want, got, tt.ok)
		}
	}
}

func TestRawCARequiresPIN(t *testing.T) {
	srv, ts := newTestServer(t)

	// No PIN.
	resp, err := http.Get(ts.URL + "/ca.pem")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-pin: status = %d, want 401", resp.StatusCode)
	}

	// Wrong PIN.
	resp, err = http.Get(ts.URL + "/ca.pem?pin=000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-pin: status = %d, want 401", resp.StatusCode)
	}

	// Correct PIN.
	resp, err = http.Get(ts.URL + "/ca.pem?pin=" + url.QueryEscape(srv.PIN()))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct-pin: status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BEGIN CERTIFICATE") {
		t.Errorf("expected PEM in body; got %q", string(body[:min(80, len(body))]))
	}
}

func TestRateLimitLockout(t *testing.T) {
	srv, ts := newTestServer(t)

	for i := range bootstrapMaxFailures {
		resp, err := http.Get(ts.URL + "/ca.pem?pin=000000")
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, resp.StatusCode)
		}
	}

	// Even the correct PIN should now be rejected with 429.
	resp, err := http.Get(ts.URL + "/ca.pem?pin=" + url.QueryEscape(srv.PIN()))
	if err != nil {
		t.Fatalf("after lockout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after lockout: status = %d, want 429", resp.StatusCode)
	}
}

func TestInstallUARedirects(t *testing.T) {
	srv, ts := newTestServer(t)

	tests := []struct {
		name     string
		ua       string
		wantPath string
	}{
		{"iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)", "/install/ios"},
		{"ipad", "Mozilla/5.0 (iPad; CPU OS 17_0)", "/install/ios"},
		{"android", "Mozilla/5.0 (Linux; Android 14)", "/install/android"},
		{"desktop", "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "/"},
	}

	client := &http.Client{
		// Don't follow redirects; we want to inspect Location header.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+"/install?pin="+srv.PIN(), nil)
			req.Header.Set("User-Agent", tt.ua)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", resp.StatusCode)
			}
			loc := resp.Header.Get("Location")
			if !strings.HasPrefix(loc, tt.wantPath) {
				t.Errorf("Location = %q, want prefix %q", loc, tt.wantPath)
			}
		})
	}
}

func TestAppleProfileFormat(t *testing.T) {
	srv, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/install/ios?pin=" + srv.PIN())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/x-apple-aspen-config"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, marker := range []string{
		`<?xml version="1.0"`,
		`<key>PayloadType</key>`,
		`<string>com.apple.security.root</string>`,
		`<string>Configuration</string>`,
	} {
		if !strings.Contains(string(body), marker) {
			t.Errorf("profile missing %q", marker)
		}
	}
}

func TestAndroidCertIsDER(t *testing.T) {
	srv, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/install/android?pin=" + srv.PIN())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if got, want := resp.Header.Get("Content-Type"), "application/x-x509-ca-cert"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	body, _ := io.ReadAll(resp.Body)
	if _, err := x509.ParseCertificate(body); err != nil {
		t.Errorf("body is not a valid x509 certificate: %v", err)
	}
}

func TestQREndpoint(t *testing.T) {
	_, ts := newTestServer(t)

	// QR is not PIN-gated.
	resp, err := http.Get(ts.URL + "/qr.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Type"), "image/png"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	body, _ := io.ReadAll(resp.Body)
	// PNG magic bytes.
	if len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("body does not start with PNG magic bytes")
	}
}

func TestRotatePIN(t *testing.T) {
	srv, ts := newTestServer(t)
	old := srv.PIN()

	// Lock the server first.
	for range bootstrapMaxFailures {
		resp, _ := http.Get(ts.URL + "/ca.pem?pin=000000")
		resp.Body.Close()
	}

	// Confirm locked.
	resp, _ := http.Get(ts.URL + "/ca.pem?pin=" + url.QueryEscape(old))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pre-rotate: expected 429, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Rotate. Should produce a different PIN and reset the counter.
	fresh := srv.RotatePIN()
	if fresh == old {
		t.Errorf("RotatePIN returned same value")
	}
	if got := srv.PIN(); got != fresh {
		t.Errorf("PIN() = %q after rotate, want %q", got, fresh)
	}

	// New PIN should now succeed.
	resp, err := http.Get(ts.URL + "/ca.pem?pin=" + url.QueryEscape(fresh))
	if err != nil {
		t.Fatalf("post-rotate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("post-rotate: expected 200, got %d", resp.StatusCode)
	}

	// Old PIN should no longer work.
	resp2, err := http.Get(ts.URL + "/ca.pem?pin=" + url.QueryEscape(old))
	if err != nil {
		t.Fatalf("post-rotate old PIN: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-rotate old PIN: expected 401, got %d", resp2.StatusCode)
	}
}

func TestIndexShowsQR(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `src="/qr.png"`) {
		t.Errorf("install page does not embed /qr.png")
	}
}
