package clientserver

import (
	"strings"
	"testing"
)

func TestRawExchangeAudit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cmd       []byte
		raw       bool
		device    string
		uid       string
		wantLevel auditLevel
		wantSub   string // must appear in the logged line
		noBytes   string // must NOT appear (the command must not be logged verbatim)
	}{
		{
			name:      "a read is informational",
			cmd:       []byte{0xFF, 0xCA, 0x00, 0x00, 0x00},
			device:    "ACS ACR122U",
			uid:       "04A1B2C3",
			wantLevel: auditInfo,
			wantSub:   "GET UID",
		},
		{
			name:      "a write is a warning",
			cmd:       []byte{0xFF, 0xD6, 0x00, 0x04, 0x04, 0xDE, 0xAD, 0xBE, 0xEF},
			device:    "ACS ACR122U",
			wantLevel: auditWarn,
			wantSub:   "UPDATE BINARY",
		},
		{
			name:      "a lock-page write carries its caution",
			cmd:       []byte{0xFF, 0xD6, 0x00, 0x03, 0x04, 0x00, 0x00, 0x00, 0x00},
			wantLevel: auditWarn,
			wantSub:   "lock/OTP",
		},
		{
			name:      "an unrecognised command is a warning",
			cmd:       []byte{0x00, 0xEE, 0x00, 0x00},
			wantLevel: auditWarn,
			wantSub:   "unrecognised",
		},
		{
			// LOAD KEY carries a 6-byte key in its data. The audit must name the
			// command without echoing the key.
			name:      "a key is not written to the log",
			cmd:       []byte{0xFF, 0x82, 0x00, 0x00, 0x06, 0xA0, 0xB1, 0xC2, 0xD3, 0xE4, 0xF5},
			wantLevel: auditInfo,
			wantSub:   "LOAD KEY",
			noBytes:   "A0B1C2D3E4F5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			level, msg := rawExchangeAudit(tc.cmd, tc.raw, tc.device, tc.uid)
			if level != tc.wantLevel {
				t.Errorf("level = %d, want %d (%q)", level, tc.wantLevel, msg)
			}
			if !strings.Contains(msg, tc.wantSub) {
				t.Errorf("line %q does not mention %q", msg, tc.wantSub)
			}
			if tc.device != "" && !strings.Contains(msg, tc.device) {
				t.Errorf("line %q does not name the device %q", msg, tc.device)
			}
			if tc.uid != "" && !strings.Contains(msg, tc.uid) {
				t.Errorf("line %q does not name the tag %q", msg, tc.uid)
			}
			// The raw hex of the command must never be in the line — even for a
			// command without secrets, so the rule cannot be broken case by case.
			if hex := upperHex(tc.cmd); strings.Contains(strings.ToUpper(msg), hex) {
				t.Errorf("line %q echoes the raw command bytes %q", msg, hex)
			}
			if tc.noBytes != "" && strings.Contains(strings.ToUpper(msg), tc.noBytes) {
				t.Errorf("line %q leaks bytes %q", msg, tc.noBytes)
			}
		})
	}
}

func upperHex(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0F]
	}
	return string(out)
}
