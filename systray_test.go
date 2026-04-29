package main

import (
	"reflect"
	"testing"
)

func TestClipboardCandidates(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		env      map[string]string
		wantCmds []string
		wantErr  bool
	}{
		{
			name:     "darwin uses pbcopy",
			goos:     "darwin",
			wantCmds: []string{"pbcopy"},
		},
		{
			name:     "windows uses clip",
			goos:     "windows",
			wantCmds: []string{"clip"},
		},
		{
			name:     "linux wayland prefers wl-copy",
			goos:     "linux",
			env:      map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			wantCmds: []string{"wl-copy"},
		},
		{
			name:     "linux x11 prefers xclip then xsel",
			goos:     "linux",
			env:      map[string]string{"DISPLAY": ":0"},
			wantCmds: []string{"xclip", "xsel"},
		},
		{
			name:     "linux dual session offers wayland first then x11",
			goos:     "linux",
			env:      map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			wantCmds: []string{"wl-copy", "xclip", "xsel"},
		},
		{
			name:     "linux headless tries everything",
			goos:     "linux",
			wantCmds: []string{"wl-copy", "xclip", "xsel"},
		},
		{
			name:    "unknown OS errors",
			goos:    "plan9",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			got, err := clipboardCandidates(tt.goos, getenv)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			gotNames := make([]string, len(got))
			for i, c := range got {
				gotNames[i] = c.name
			}
			if !reflect.DeepEqual(gotNames, tt.wantCmds) {
				t.Errorf("candidate names = %v, want %v", gotNames, tt.wantCmds)
			}
		})
	}
}
