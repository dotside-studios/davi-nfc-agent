package e2e

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
	"github.com/gorilla/websocket"
)

// apiSecret is the shared secret every agent here runs with. Loopback bypasses
// it, so it proves nothing on its own; it is set because a deployment has one.
const apiSecret = "e2e-shared-secret"

// timeout bounds every wait. Generous on purpose: these tests bind real
// listeners and poll a reader every 100ms.
const timeout = 10 * time.Second

type options struct {
	// Tags are on the reader from the moment it opens.
	Tags []nfc.Tag

	// Pairing runs the pairing server, so a test can obtain a device credential.
	Pairing bool
}

// harness is a running agent and the handles a test drives it by.
type harness struct {
	Agent   *agent.Agent
	Runtime *agent.Runtime

	// Devices is the phone driver the test built and handed over, Servers what
	// the agent is served from, and Pairing the pairing plugin, when the test
	// asked for one.
	Devices *remotenfc.Manager
	Servers *agent.ServerPlugin
	Pairing *agent.PairingPlugin

	// Hardware is the reader the agent opened, for presenting and removing tags.
	Hardware *nfc.MockDevice

	// Origin is the agent's https base, and Pair the same: pairing is served
	// from the agent's listener. Bootstrap is the cleartext CA-distribution
	// listener.
	Origin    string
	Pair      string
	Bootstrap string

	scans chan nfc.NFCData
}

// start builds and starts an agent the way docs/custom-builds.md does.
func start(t *testing.T, opts options) *harness {
	t.Helper()

	o := agent.DefaultOptions()
	o.ConfigDir = t.TempDir()
	o.APISecret = apiSecret

	// Not the default port, so these tests can run beside an agent already
	// running on this machine.
	o.DevicePort = freePort(t)

	// The agent names no device protocol: what the devices scan and what they
	// hold reach it through the manager below, and their endpoint is mounted
	// alongside the clients.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)

	// Stands in for nfc/pcsc, the one part a test cannot supply.
	hardware := nfc.NewMockManager()
	hardware.MockDevice.SetTags(opts.Tags)

	rt, err := agent.Setup(o, multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: hardware},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// The listener is a plugin, as it is in docs/custom-builds.md. With none
	// registered the agent drives the reader and serves nothing. Pairing is a
	// plugin of its own, on a listener of its own; with no tray here its menu
	// entries go to a menu that draws nothing. The certificate the listener
	// serves and the authority pairing hands out are the trust plugin's, not
	// the agent's.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}
	servers := &agent.ServerPlugin{
		Config:         listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
		Certificates:   rt.Certificates,
		AllowedOrigins: rt.AllowedOrigins,
	}
	servers.ServeMode = map[string]http.Handler{
		server.ModeClient: clientserver.New(clientserver.Config{
			APISecret:            rt.Agent.APISecret,
			OriginPolicy:         servers.OriginPolicy(),
			TokenVerifier:        rt.Agent.TokenVerifier(),
			Tags:                 rt.Agent,
			AllowTagModification: rt.Agent.TagModificationAllowed,
			Scans:                &rt.Agent.Events().Tag,
			ReaderStatus:         &rt.Agent.Events().Reader,
		}),
		server.ModeDevice: devices.Handler(remotenfc.ServerOptions{
			Authenticate:         servers.Authenticate(),
			CheckOrigin:          servers.CheckOrigin(),
			AllowTagModification: rt.Agent.TagModificationAllowed,
			PublicKeyPin:         rt.Agent.PublicKeyPin,
			Revocations:          rt.Agent.Devices(),
		}),
	}

	// Pairing is served from the agent's listener, which already serves the
	// certificate the key pin covers. The bootstrap listener is cleartext and
	// hands out the CA, so no credential is issued over it. The endpoint is
	// registered before the server plugin activates, which is when the routes
	// are mounted.
	var pairing *agent.PairingPlugin
	if opts.Pairing {
		pairing = agent.NewPairingPlugin(rt.Agent, freePort(t), rt.Certificates)
		servers.Add(agent.Endpoint{
			Name:    "pairing",
			Pattern: "/pair",
			Handler: pairing.Server.Server().PairHandler(),
		})
	}

	if err := rt.Agent.Plugins.Add(servers, trust); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	if pairing != nil {
		if err := rt.Agent.Plugins.Add(pairing); err != nil {
			t.Fatalf("Plugins.Add: %v", err)
		}
	}

	h := &harness{
		Agent:    rt.Agent,
		Runtime:  rt,
		Devices:  devices,
		Servers:  servers,
		Hardware: hardware.MockDevice,
		Pairing:  pairing,
		Origin:   "https://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(rt.Agent.DevicePort())),
		scans:    make(chan nfc.NFCData, 32),
	}
	if pairing != nil {
		// Pairing is on the agent's listener; Bootstrap is the cleartext
		// listener that hands out the CA.
		h.Pair = h.Origin
		h.Bootstrap = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(pairing.Port()))
	}

	rt.Agent.Events().Tag.Connect(func(data nfc.NFCData) {
		select {
		case h.scans <- data:
		default:
		}
	})

	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(rt.Agent.Shutdown)

	if h.Pair != "" {
		// The pairing server serves on a goroutine and does not bind before
		// Start returns, so a request sent immediately can beat it there.
		waitForListener(t, h.Bootstrap[len("http://"):])
	}

	return h
}

// reopenHardware puts the mock reader back in the state a PC/SC handle is in
// when it is opened again. The mock holds one device and hands the same closed
// one back, so a restart would otherwise read from a closed handle. Safe to
// call only while the agent is stopped, which is when a reader is reopened.
func (h *harness) reopenHardware() {
	h.Hardware.IsOpen = true
}

// observed waits for the next scan the agent reports.
func (h *harness) observed(t *testing.T) nfc.NFCData {
	t.Helper()

	select {
	case data := <-h.scans:
		return data
	case <-time.After(timeout):
		t.Fatal("no scan reached the observer registered before Start")
		return nfc.NFCData{}
	}
}

// ws turns the agent's https base into the wss one its endpoints live on.
func (h *harness) ws(path string) string {
	return "wss" + h.Origin[len("https"):] + path
}

// tlsConfig authenticates the agent by its public key rather than by a chain,
// which is how a phone recognises it. Every connection here goes through it, so
// a certificate that does not carry the pinned key fails the whole suite.
func (h *harness) tlsConfig(t *testing.T) *tls.Config {
	t.Helper()

	want := h.Agent.PublicKeyPin()
	if want == "" {
		t.Fatal("the agent reported no public key pin; a device would have nothing to recognise it by")
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verified below against the pin instead.
		InsecureSkipVerify: true, //nolint:gosec // VerifyPeerCertificate pins the key
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("the agent served no certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("parse the agent's certificate: %w", err)
			}
			if got := publicKeyPin(cert); got != want {
				return fmt.Errorf("the agent served key %s, want the pinned %s", got, want)
			}
			return nil
		},
	}
}

// publicKeyPin computes serverInfo.publicKeyPin from a served certificate.
func publicKeyPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
}

func (h *harness) httpClient(t *testing.T) *http.Client {
	t.Helper()

	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: h.tlsConfig(t)},
	}
}

// dial opens a WebSocket to the agent, returning the handshake response so a
// test can read the status of a refusal.
func (h *harness) dial(t *testing.T, path string, subprotocols []string) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	dialer := &websocket.Dialer{
		TLSClientConfig:  h.tlsConfig(t),
		HandshakeTimeout: timeout,
		Subprotocols:     subprotocols,
	}
	conn, resp, err := dialer.Dial(h.ws(path), nil)
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}
	return conn, resp, err
}

// client connects as an application does: plain /ws, carrying the secret.
//
// It returns once the agent has the connection, not once the handshake is
// answered: registration happens on the agent's own goroutine, and a scan
// broadcast before it lands reaches nobody.
func (h *harness) client(t *testing.T) *websocket.Conn {
	t.Helper()

	// Signal.Connect rather than Connect: the property would replay the
	// current count before the dial, and the wait below would pass on it.
	registered := make(chan struct{}, 1)
	sub := h.Servers.Events().Clients.Signal.Connect(func(int) {
		select {
		case registered <- struct{}{}:
		default:
		}
	})
	defer sub.Disconnect()

	conn, resp, err := h.dial(t, "/ws?secret="+apiSecret, nil)
	if err != nil {
		t.Fatalf("client dial: %v (status %s)", err, status(resp))
	}

	select {
	case <-registered:
	case <-time.After(timeout):
		t.Fatal("the agent never reported the client connected")
	}
	return conn
}

// phone connects as a device does and completes the v1 handshake, returning the
// connection, the assigned device ID, and the pin the agent reported.
func (h *harness) phone(t *testing.T, credential string, caps map[string]any) (*websocket.Conn, string, string) {
	t.Helper()

	conn, resp, err := h.dial(t, "/ws?mode=device&secret="+credential, remotenfc.DeviceSubprotocols)
	if err != nil {
		t.Fatalf("device dial: %v (status %s)", err, status(resp))
	}

	send(t, conn, protocol.WebSocketRequest{
		ID:   "hello-1",
		Type: remotenfc.WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": 1,
			"deviceName":      "E2E Phone",
			"platform":        "android",
			"appVersion":      "1.0.0",
			"capabilities":    caps,
		},
	})

	var hello struct {
		ProtocolVersion int    `json:"protocolVersion"`
		DeviceID        string `json:"deviceID"`
		ServerInfo      struct {
			PublicKeyPin string `json:"publicKeyPin"`
		} `json:"serverInfo"`
	}
	reply := awaitFrame(t, conn, remotenfc.WSTypeHelloResponse)
	if !reply.Success {
		t.Fatalf("the agent refused the handshake: %s", reply.Error)
	}
	decode(t, reply.Payload, &hello)
	if hello.DeviceID == "" {
		t.Fatalf("the handshake assigned no device ID: %s", reply.Payload)
	}

	return conn, hello.DeviceID, hello.ServerInfo.PublicKeyPin
}

// phoneCapabilities is a phone that can do anything a client might ask of it.
func phoneCapabilities() map[string]any {
	return map[string]any{
		"canRead":           true,
		"canWrite":          true,
		"canLock":           true,
		"canTransceive":     true,
		"nfcType":           "nfca",
		"deviceType":        "smartphone",
		"supportedTagTypes": []string{"NTAG", "MIFARE Ultralight"},
	}
}

// frame is one message off a socket, flattened so responses and broadcasts can
// be read by the same loop, which is what a real client does.
type frame struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Payload json.RawMessage `json:"payload"`
}

// awaitFrame reads until a message of the given type arrives, skipping the
// unrelated traffic sharing the connection.
func awaitFrame(t *testing.T, conn *websocket.Conn, msgType string) frame {
	t.Helper()

	return await(t, conn, fmt.Sprintf("a %q message", msgType), func(f frame) bool {
		return f.Type == msgType
	})
}

// awaitReply reads until the response to a request arrives. Matching on the ID
// rather than the type catches a reply correlated to the wrong request.
func awaitReply(t *testing.T, conn *websocket.Conn, requestID string) frame {
	t.Helper()

	return await(t, conn, fmt.Sprintf("the reply to %q", requestID), func(f frame) bool {
		return f.ID == requestID
	})
}

func await(t *testing.T, conn *websocket.Conn, want string, match func(frame) bool) frame {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	var seen []string
	for {
		var f frame
		if err := conn.ReadJSON(&f); err != nil {
			t.Fatalf("waiting for %s: %v (saw %v)", want, err, seen)
		}
		if match(f) {
			return f
		}
		seen = append(seen, f.Type)
	}
}

func send(t *testing.T, conn *websocket.Conn, req protocol.WebSocketRequest) {
	t.Helper()

	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("send %s: %v", req.Type, err)
	}
}

func decode(t *testing.T, payload json.RawMessage, into any) {
	t.Helper()

	if err := json.Unmarshal(payload, into); err != nil {
		t.Fatalf("decode payload %s: %v", payload, err)
	}
}

// status describes a handshake response, including the nil a failed dial gives.
func status(resp *http.Response) string {
	if resp == nil {
		return "no response"
	}
	return resp.Status
}

// waitForListener blocks until something accepts on addr.
func waitForListener(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing is listening on %s after %s: %v", addr, timeout, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// freePort reserves a port by binding it and letting it go.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}
