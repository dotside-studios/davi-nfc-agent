package tls

import (
	"strings"
	"testing"
)

// The URI is the out-of-band channel: everything a device needs to reach this
// agent and recognise it must survive the round trip through a QR.
func TestPairingURIRoundTrip(t *testing.T) {
	want := PairingURI{
		Host:    "192.0.2.7",
		Port:    9472,
		SPKI:    "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
		Code:    "123456",
		AppName: "Davi NFC Agent",
	}

	raw := want.String()
	if !strings.HasPrefix(raw, PairingScheme+"://") {
		t.Fatalf("URI = %q, want the %s scheme", raw, PairingScheme)
	}

	got, err := ParsePairingURI(raw)
	if err != nil {
		t.Fatalf("ParsePairingURI(%q): %v", raw, err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// The pin contains '/' and '+', which must not escape the query.
func TestPairingURIEscapesThePin(t *testing.T) {
	uri := PairingURI{Host: "host", Port: 1, SPKI: "sha256/a+b/c="}

	got, err := ParsePairingURI(uri.String())
	if err != nil {
		t.Fatalf("ParsePairingURI: %v", err)
	}
	if got.SPKI != uri.SPKI {
		t.Errorf("SPKI = %q, want %q", got.SPKI, uri.SPKI)
	}
}

func TestParsePairingURIRejectsOtherSchemes(t *testing.T) {
	for _, raw := range []string{"http://host:9472/?code=1", "not a uri at all", PairingScheme + "://host/?code=1"} {
		if _, err := ParsePairingURI(raw); err == nil {
			t.Errorf("ParsePairingURI(%q) succeeded, want an error", raw)
		}
	}
}

// The QR is rendered here so it can be read off the kiosk screen. One fetched
// over the network is only as trustworthy as the connection that served it.
func TestPairingURIRendersLocally(t *testing.T) {
	uri := PairingURI{Host: "192.0.2.7", Port: 9472, SPKI: "sha256/abc=", Code: "123456"}

	art, err := uri.TerminalQR()
	if err != nil {
		t.Fatalf("TerminalQR: %v", err)
	}
	if len(art) == 0 || !strings.Contains(art, "\n") {
		t.Errorf("TerminalQR produced %d bytes, want a block of text", len(art))
	}
}
