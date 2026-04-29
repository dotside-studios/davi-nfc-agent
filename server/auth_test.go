package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackRequest(t *testing.T) {
	tests := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:50000", true},
		{"127.0.0.55:9999", true},
		{"[::1]:9000", true},
		{"192.168.1.5:9470", false},
		{"10.0.0.5:9470", false},
		{"[2001:db8::1]:9000", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.RemoteAddr = tt.remote
			if got := IsLoopbackRequest(r); got != tt.want {
				t.Errorf("IsLoopbackRequest(%q) = %v, want %v", tt.remote, got, tt.want)
			}
		})
	}
}

func TestCheckAPISecret(t *testing.T) {
	const want = "supersecret"

	tests := []struct {
		name       string
		secret     string
		query      string
		authHeader string
		remote     string
		wantOK     bool
		wantStatus int
	}{
		{name: "no secret configured allows all", secret: "", query: "", remote: "1.2.3.4:5", wantOK: true},
		{name: "loopback bypass", secret: want, query: "", remote: "127.0.0.1:5", wantOK: true},
		{name: "matching query param", secret: want, query: want, remote: "1.2.3.4:5", wantOK: true},
		{name: "matching bearer header", secret: want, authHeader: "Bearer " + want, remote: "1.2.3.4:5", wantOK: true},
		{name: "wrong query", secret: want, query: "wrong", remote: "1.2.3.4:5", wantOK: false, wantStatus: http.StatusUnauthorized},
		{name: "no secret presented", secret: want, remote: "1.2.3.4:5", wantOK: false, wantStatus: http.StatusUnauthorized},
		{name: "wrong bearer", secret: want, authHeader: "Bearer wrong", remote: "1.2.3.4:5", wantOK: false, wantStatus: http.StatusUnauthorized},
		{name: "non-bearer authorization ignored", secret: want, authHeader: "Basic supersecret", remote: "1.2.3.4:5", wantOK: false, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.RemoteAddr = tt.remote
			if tt.query != "" {
				q := r.URL.Query()
				q.Set("secret", tt.query)
				r.URL.RawQuery = q.Encode()
			}
			if tt.authHeader != "" {
				r.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			got := CheckAPISecret(w, r, tt.secret)
			if got != tt.wantOK {
				t.Errorf("CheckAPISecret = %v, want %v", got, tt.wantOK)
			}
			if !tt.wantOK && w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
