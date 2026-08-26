package server

import (
	"os"
	"testing"
)

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single host:port", "localhost:3002", []string{"localhost:3002"}},
		{"comma separated with spaces", "a.example:443, b.example:8080", []string{"a.example:443", "b.example:8080"}},
		{"blank entries dropped", "a.example,, ,b.example", []string{"a.example", "b.example"}},
		{"wildcard preserved", "*", []string{"*"}},
		// People paste the origin they see in a browser. Reducing it to host:port
		// is the difference between a working entry and one that is silently
		// ignored, which looks identical to never having configured it.
		{"full url reduced to host", "https://order.davi.social", []string{"order.davi.social"}},
		{"url with port and path", "https://order.davi.social:8443/app", []string{"order.davi.social:8443"}},
		{"trailing slash trimmed", "order.davi.social/", []string{"order.davi.social"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseAllowedOrigins(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseAllowedOrigins(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseAllowedOrigins(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseAllowedOriginsFallsBackToEnv(t *testing.T) {
	t.Setenv("DAVI_NFC_ALLOWED_ORIGINS", "env.example:443")

	if got := ParseAllowedOrigins(""); len(got) != 1 || got[0] != "env.example:443" {
		t.Errorf("ParseAllowedOrigins(\"\") = %v, want [env.example:443]", got)
	}

	// An explicit flag wins over the environment.
	if got := ParseAllowedOrigins("flag.example:443"); len(got) != 1 || got[0] != "flag.example:443" {
		t.Errorf("flag did not take precedence: got %v", got)
	}

	_ = os.Unsetenv("DAVI_NFC_ALLOWED_ORIGINS")
}
