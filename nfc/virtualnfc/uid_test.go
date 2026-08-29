package virtualnfc

import "testing"

func TestParseUID(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "colon", in: "04:AB:CD:EF", want: "04:AB:CD:EF"},
		{name: "bare", in: "04ABCDEF", want: "04:AB:CD:EF"},
		{name: "spaces", in: "04 ab cd ef", want: "04:AB:CD:EF"},
		{name: "dashes lowercase", in: "04-ab-cd-ef", want: "04:AB:CD:EF"},
		{name: "seven bytes", in: "04a1b2c3d4e5f6", want: "04:A1:B2:C3:D4:E5:F6"},
		{name: "empty", in: "", wantErr: true},
		{name: "odd length", in: "04A", wantErr: true},
		{name: "non-hex", in: "04:GG", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseUID(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseUID(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUID(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseUID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
