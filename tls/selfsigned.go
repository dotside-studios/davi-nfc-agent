package tls

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// leafKeyFile holds the agent's long-lived private key.
//
// The key outlives any individual certificate on purpose. A certificate is
// reissued whenever the host's addresses change, and a device that pinned the
// agent would be locked out every time that happened if the key changed with
// it. Keeping the key stable means the pin survives reissuance.
const leafKeyFile = "agent.key"

// certValidity is how long a generated leaf is good for. Long enough that a
// kiosk left running does not expire mid-shift, short enough to stay unhelpful
// to anyone who lifts the key.
const certValidity = 825 * 24 * time.Hour

// loadOrCreateLeafKey returns the agent's persistent private key, generating it
// on first use.
func (m *Manager) loadOrCreateLeafKey() (*ecdsa.PrivateKey, error) {
	path := m.leafKeyPath()

	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("agent key is not valid PEM")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse agent key: %w", err)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read agent key: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate agent key: %w", err)
	}

	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode agent key: %w", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return nil, fmt.Errorf("write agent key: %w", err)
	}
	_ = SecureFile(path)

	return key, nil
}

func (m *Manager) leafKeyPath() string {
	return filepath.Join(m.tlsDir, leafKeyFile)
}

// generateSelfSigned issues a self-signed certificate for hosts using the
// agent's persistent key, and writes it as the server cert/key pair.
//
// This is the default: it involves no certificate authority and touches no
// trust store. Devices authenticate the agent by pinning its public key, which
// they learn when pairing.
func (m *Manager) generateSelfSigned(hosts []string) error {
	return m.issueLeaf(hosts, nil, nil)
}

// issueLeaf issues a certificate for hosts over the agent's persistent key and
// writes it as the server cert/key pair. A nil parent self-signs it; otherwise
// it is signed by that certificate and key.
//
// Both routes sign the same key, which is what keeps PublicKeyPin honest: the
// pin handed out at pairing is over the key the listener presents, whether or
// not this install has a certificate authority.
func (m *Manager) issueLeaf(hosts []string, parent *x509.Certificate, parentKey crypto.Signer) error {
	key, err := m.loadOrCreateLeafKey()
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	// Apple rejects a server certificate whose total validity exceeds 825 days,
	// so the window is anchored to NotBefore rather than to now.
	notBefore := time.Now().Add(-time.Hour)

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Davi NFC Agent"},
			CommonName:   "Davi NFC Agent",
		},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}

	issuer, issuerKey := parent, parentKey
	if issuer == nil {
		issuer, issuerKey = &template, key
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(m.certFile, certPEM, 0644); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(m.keyFile, keyPEM, 0600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	_ = SecureFile(m.keyFile)

	return nil
}

// PublicKeyPin returns the base64 SHA-256 of the agent's SubjectPublicKeyInfo,
// in the same form as an HTTP Public-Key-Pins value.
//
// This identifies the agent across certificate reissues, which is what makes it
// pinnable: the certificate changes when the host's addresses do, the key does
// not. Devices compare this value, learned at pairing, on every connection.
func (m *Manager) PublicKeyPin() (string, error) {
	key, err := m.loadOrCreateLeafKey()
	if err != nil {
		return "", err
	}

	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", fmt.Errorf("encode public key: %w", err)
	}

	sum := sha256.Sum256(spki)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// servedCertMatchesPin reports whether the certificate on disk carries the key
// PublicKeyPin describes.
//
// Where it does not, every device that pairs is handed a pin the handshake can
// never satisfy, and no amount of re-pairing helps. Installs issued before the
// CA route signed the persistent key are in that state, so this is checked on
// startup rather than assumed.
func (m *Manager) servedCertMatchesPin() bool {
	key, err := m.loadOrCreateLeafKey()
	if err != nil {
		return false
	}

	data, err := os.ReadFile(m.certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	served, ok := cert.PublicKey.(*ecdsa.PublicKey)
	return ok && served.Equal(&key.PublicKey)
}
