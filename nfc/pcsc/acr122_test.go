package pcsc

import (
	"bytes"
	"testing"
)

func TestACR122BlinkCommand(t *testing.T) {
	tests := []struct {
		name  string
		blink acr122Blink
		want  []byte
	}{
		{
			// One green blink with a beep over its first half, leaving the LED
			// as the reader had it: the blink mask is set, the state mask is not.
			name:  "success",
			blink: acr122Success,
			want:  []byte{0xFF, 0x00, 0x40, 0xA0, 0x04, 0x01, 0x01, 0x01, 0x01},
		},
		{
			name:  "failure",
			blink: acr122Failure,
			want:  []byte{0xFF, 0x00, 0x40, 0x50, 0x04, 0x01, 0x01, 0x02, 0x01},
		},
		{
			// Every bit of the state byte, to pin the bit order down: the
			// reader reads the low nibble as the state after blinking and the
			// high nibble as the blinking itself.
			name: "all bits",
			blink: acr122Blink{
				led: acr122FinalRedOn | acr122FinalGreenOn |
					acr122FinalRedMask | acr122FinalGreenMask |
					acr122BlinkRedOn | acr122BlinkGreenOn |
					acr122BlinkRedMask | acr122BlinkGreenMask,
				t1:     0x02,
				t2:     0x03,
				repeat: 0x04,
				buzzer: acr122BuzzerBoth,
			},
			want: []byte{0xFF, 0x00, 0x40, 0xFF, 0x04, 0x02, 0x03, 0x04, 0x03},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.blink.command(); !bytes.Equal(got, tt.want) {
				t.Errorf("command() = % X, want % X", got, tt.want)
			}
		})
	}
}

func TestHasACR122LEDBuzzer(t *testing.T) {
	tests := []struct {
		reader string
		want   bool
	}{
		{"ACS ACR122U PICC Interface 00 00", true},
		{"ACS ACR122U PICC Interface", true},
		{"Touchatag ACR122U", true},
		{"acs acr122u picc interface", true},
		{"ACS ACR1252 1S CL Reader", false},
		{"Identiv uTrust 3700 F CL Reader", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.reader, func(t *testing.T) {
			if got := hasACR122LEDBuzzer(tt.reader); got != tt.want {
				t.Errorf("hasACR122LEDBuzzer(%q) = %v, want %v", tt.reader, got, tt.want)
			}
		})
	}
}

func TestCheckACR122Response(t *testing.T) {
	tests := []struct {
		name    string
		resp    []byte
		wantErr bool
	}{
		// The second byte reports the LED states rather than a status, so any
		// value of it is a success as long as the first byte is 0x90.
		{"leds off", []byte{0x90, 0x00}, false},
		{"green lit", []byte{0x90, 0x02}, false},
		{"refused", []byte{0x63, 0x00}, true},
		{"short", []byte{0x90}, true},
		{"empty", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkACR122Response(tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkACR122Response(% X) error = %v, wantErr %v", tt.resp, err, tt.wantErr)
			}
		})
	}
}
