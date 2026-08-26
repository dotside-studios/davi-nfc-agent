package tls

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/skip2/go-qrcode"
)

// PairingScheme is the URI scheme a device handles to pair.
const PairingScheme = "davi-pair"

// PairingURI is what a device needs to reach this agent and recognise it before
// trusting anything it says.
//
// The key pin used to arrive only in the pairing response, over the channel it
// exists to protect. Carried here instead, on a QR read off the kiosk screen,
// it authenticates the channel the credential is issued over.
type PairingURI struct {
	Host    string // Address the device connects to
	Port    int    // Pairing port
	SPKI    string // PublicKeyPin the device pins the TLS connection to
	Code    string // Pairing PIN, proof the holder can see the kiosk
	AppName string
}

// String renders the URI encoded in the QR:
//
//	davi-pair://host:port/?spki=sha256%2F...&code=123456&name=Davi
func (p PairingURI) String() string {
	q := url.Values{}
	if p.SPKI != "" {
		q.Set("spki", p.SPKI)
	}
	if p.Code != "" {
		q.Set("code", p.Code)
	}
	if p.AppName != "" {
		q.Set("name", p.AppName)
	}

	u := url.URL{
		Scheme:   PairingScheme,
		Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
		Path:     "/",
		RawQuery: q.Encode(),
	}
	return u.String()
}

// ParsePairingURI reads back what String wrote. For tests and for anything
// implementing the device side in Go.
func ParsePairingURI(raw string) (PairingURI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return PairingURI{}, fmt.Errorf("parse pairing URI: %w", err)
	}
	if u.Scheme != PairingScheme {
		return PairingURI{}, fmt.Errorf("pairing URI scheme is %q, want %q", u.Scheme, PairingScheme)
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return PairingURI{}, fmt.Errorf("pairing URI host: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return PairingURI{}, fmt.Errorf("pairing URI port: %w", err)
	}

	q := u.Query()
	return PairingURI{
		Host:    host,
		Port:    port,
		SPKI:    q.Get("spki"),
		Code:    q.Get("code"),
		AppName: q.Get("name"),
	}, nil
}

// TerminalQR renders the URI as text, to print where the operator can read it
// off the screen.
//
// Rendering locally is the point: a QR fetched over the network is only as
// trustworthy as the connection that served it, which is the connection the pin
// exists to authenticate.
func (p PairingURI) TerminalQR() (string, error) {
	code, err := qrcode.New(p.String(), qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("encode pairing QR: %w", err)
	}
	return code.ToSmallString(false), nil
}
