package ntag424

import (
	"bytes"
	"errors"
	"testing"
)

// zeroKeys is the factory-default key pair the published examples use.
var zeroKeys = Keys{MetaRead: zeroKey, FileRead: zeroKey}

// The published SUN URLs, verified end to end. Two come from AN12196 itself and
// two from the reference SDM backend's live demo, which runs the same
// factory-default keys. Between them they cover every layout this package
// claims to read.
func TestVerifyURLAgainstPublishedTaps(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		uid     string
		counter uint32
		data    string // decrypted file data, empty when the tag mirrors none
	}{
		{
			// AN12196 section 3.4.4.1.
			name:    "short parameter names, no file data",
			url:     "https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337086",
			uid:     "04DE5F1EACC040",
			counter: 0x3D,
		},
		{
			// AN12196 section 3.4.3.1.
			name:    "encrypted file data",
			url:     "https://my424dna.com/?picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842B&cmac=C48B89C17A233B2C",
			uid:     "04958CAA5C5E80",
			counter: 1,
			data:    "78787878787878787878787878787878",
		},
		{
			name:    "the reference backend's encrypted PICCData example",
			url:     "https://sdm.nfcdeveloper.com/tag?picc_data=EF963FF7828658A599F3041510671E88&cmac=94EED9EE65337086",
			uid:     "04DE5F1EACC040",
			counter: 0x3D,
		},
		{
			name:    "the reference backend's file data example",
			url:     "https://sdm.nfcdeveloper.com/tag?picc_data=FD91EC264309878BE6345CBE53BADF40&enc=CEE9A53E3E463EF1F459635736738962&cmac=ECC1E7F6C6C73BF6",
			uid:     "04958CAA5C5E80",
			counter: 8,
			data:    "78787878787878787878787878787878",
		},
		{
			// The UID and counter mirrored in the clear, with the MAC still
			// proving the tap.
			name:    "plain UID and counter",
			url:     "https://sdm.nfcdeveloper.com/tagpt?uid=041E3C8A2D6B80&ctr=000006&cmac=4B00064004B0B3D3",
			uid:     "041E3C8A2D6B80",
			counter: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tap, err := VerifyURL(tt.url, zeroKeys)
			if err != nil {
				t.Fatalf("VerifyURL: %v", err)
			}
			if got := tap.UIDString(); got != tt.uid {
				t.Errorf("UID = %s, want %s", got, tt.uid)
			}
			if tap.ReadCounter != tt.counter {
				t.Errorf("ReadCounter = %d, want %d", tap.ReadCounter, tt.counter)
			}
			if tt.data == "" {
				if tap.FileData != nil {
					t.Errorf("FileData = %X, want none", tap.FileData)
				}
				return
			}
			if want := mustHex(t, tt.data); !bytes.Equal(tap.FileData, want) {
				t.Errorf("FileData = %X, want %X", tap.FileData, want)
			}
		})
	}
}

// A URL edited after the tap no longer verifies. Each case changes one thing a
// forger would: the counter, the encrypted payload, the MAC, or the file data
// the MAC covers.
func TestVerifyURLRefusesAnEditedTap(t *testing.T) {
	tests := map[string]string{
		"a MAC from another tap": "https://my424dna.com/?picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842B&cmac=94EED9EE65337086",
		"altered file data":      "https://my424dna.com/?picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842C&cmac=C48B89C17A233B2C",
		"a flipped MAC bit":      "https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337087",
		"a bumped plain counter": "https://sdm.nfcdeveloper.com/tagpt?uid=041E3C8A2D6B80&ctr=000007&cmac=4B00064004B0B3D3",
		"another tag's UID":      "https://sdm.nfcdeveloper.com/tagpt?uid=041E3C8A2D6B81&ctr=000006&cmac=4B00064004B0B3D3",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyURL(raw, zeroKeys); !errors.Is(err, ErrMACMismatch) && !errors.Is(err, ErrPICCData) {
				t.Errorf("err = %v, want a verification failure", err)
			}
		})
	}
}

// A URL from a tag whose keys are not these is refused, which is what tells one
// deployment's tags from another's.
func TestVerifyURLRefusesAnotherDeploymentsKeys(t *testing.T) {
	other := make([]byte, KeySize)
	other[15] = 1

	_, err := VerifyURL(
		"https://sdm.nfcdeveloper.com/tagpt?uid=041E3C8A2D6B80&ctr=000006&cmac=4B00064004B0B3D3",
		Keys{MetaRead: zeroKey, FileRead: other},
	)
	if !errors.Is(err, ErrMACMismatch) {
		t.Errorf("err = %v, want ErrMACMismatch", err)
	}
}

// The MAC covers the URL text, so it has to be read out of the raw query rather
// than rebuilt from decoded values. This pins the span: the encrypted data, the
// separator, and the MAC parameter's own name.
func TestMACInputIsTheURLText(t *testing.T) {
	data, err := ParseURL("https://my424dna.com/?picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842B&cmac=C48B89C17A233B2C")
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	if want := "94592FDE69FA06E8E3B6CA686A22842B&cmac="; string(data.MACInput) != want {
		t.Errorf("MACInput = %q, want %q", data.MACInput, want)
	}
}

// A URL that never came from a tag is reported as such, rather than as a failed
// verification: the caller answers the two differently.
func TestParseURLRejectsWhatIsNotATap(t *testing.T) {
	tests := map[string]string{
		"no parameters at all":   "https://example.com/",
		"no MAC":                 "https://example.com/?picc_data=EF963FF7828658A599F3041510671E88",
		"a MAC and nothing else": "https://example.com/?cmac=94EED9EE65337086",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := VerifyURL(raw, zeroKeys)
			if !errors.Is(err, ErrNotSDM) {
				t.Errorf("err = %v, want ErrNotSDM", err)
			}
		})
	}

	t.Run("parameters that are not hex", func(t *testing.T) {
		_, err := ParseURL("https://example.com/?picc_data=nothex&cmac=94EED9EE65337086")
		if err == nil {
			t.Error("a non-hex parameter was accepted")
		}
	})

	t.Run("a MAC of the wrong length", func(t *testing.T) {
		_, err := ParseURL("https://example.com/?picc_data=EF963FF7828658A599F3041510671E88&cmac=94EE")
		if err == nil {
			t.Error("a short MAC was accepted")
		}
	})
}

// A parameter name must match a whole parameter. "c" is a MAC name and appears
// inside "picc_data", so a substring match would take the MAC input from the
// wrong offset and fail every tap on a tag using long names.
func TestParameterNamesMatchWholeParameters(t *testing.T) {
	raw := "picc_data=FDE4AFA99B5C820A2C1BB0F1C792D0EB&enc=94592FDE69FA06E8E3B6CA686A22842B&cmac=C48B89C17A233B2C"
	got, ok := valueOffset(raw, defaultNames.MAC)
	if !ok {
		t.Fatal("the MAC parameter was not found")
	}
	if want := len(raw) - len("C48B89C17A233B2C"); got != want {
		t.Errorf("MAC value starts at %d, want %d (matched inside another parameter)", got, want)
	}
}

// A tag configured with its own parameter names is read by naming them.
func TestVerifyURLWithCustomNames(t *testing.T) {
	names := Names{
		PICCData: []string{"p"},
		MAC:      []string{"m"},
	}
	tap, err := VerifyURLWith(
		"https://example.com/t?p=EF963FF7828658A599F3041510671E88&m=94EED9EE65337086",
		zeroKeys, names,
	)
	if err != nil {
		t.Fatalf("VerifyURLWith: %v", err)
	}
	if got := tap.UIDString(); got != "04DE5F1EACC040" {
		t.Errorf("UID = %s, want 04DE5F1EACC040", got)
	}
}
