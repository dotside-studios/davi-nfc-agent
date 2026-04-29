package server

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name    string
		extra   []string
		host    string
		origin  string
		want    bool
	}{
		{"empty origin allowed (native apps)", nil, "kiosk:9470", "", true},
		{"same host:port", nil, "kiosk:9470", "http://kiosk:9470", true},
		{"same host case-insensitive", nil, "Kiosk:9470", "http://kiosk:9470", true},
		{"different host", nil, "kiosk:9470", "https://evil.example", false},
		{"different port on same host", nil, "kiosk:9470", "http://kiosk:80", false},
		{"allowlisted host:port", []string{"dashboard:8080"}, "kiosk:9470", "https://dashboard:8080", true},
		{"allowlisted host without port", []string{"dashboard:8080"}, "kiosk:9470", "https://dashboard:9999", false},
		{"wildcard allows everything", []string{"*"}, "kiosk:9470", "https://random.example", true},
		{"malformed origin rejected", nil, "kiosk:9470", "::not a url::", false},
		{"loopback v4 same as request host", nil, "127.0.0.1:9470", "http://127.0.0.1:9470", true},
		{"loopback v6 same as request host", nil, "[::1]:9470", "http://[::1]:9470", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := CheckOrigin(tt.extra)
			r := httpReq(t, tt.host, tt.origin)
			if got := fn(r); got != tt.want {
				t.Errorf("CheckOrigin(host=%q, origin=%q, extra=%v) = %v, want %v", tt.host, tt.origin, tt.extra, got, tt.want)
			}
		})
	}
}

func httpReq(t *testing.T, host, origin string) *http.Request {
	t.Helper()
	r, err := http.NewRequest("GET", "http://"+host+"/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}
