package webui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// Screenshot harness. Serves the real control handler and the embedded console
// over a seeded fake host, so the UI can be driven without an agent or hardware.
//
// Not part of the normal suite: it only runs when SCREENSHOT_ADDR is set.
//
//	SCREENSHOT_ADDR=127.0.0.1:9911 go test ./webui/ -run TestScreenshotHarness -timeout 20m
func TestScreenshotHarness(t *testing.T) {
	addr := os.Getenv("SCREENSHOT_ADDR")
	if addr == "" {
		t.Skip("set SCREENSHOT_ADDR to run the screenshot harness")
	}

	host := newFakeHost()
	host.configDir = "/home/operator/.config/davi-nfc-agent"
	host.available = []string{"ACS ACR1252U 01 00"}
	host.cardTypes = []string{
		"MIFARE Classic 1K", "MIFARE Classic 4K", "MIFARE Ultralight",
		"NTAG213", "NTAG215", "NTAG216", "DESFire", "Type4",
	}
	host.apiSecret = "s3cr3t-0f-the-agent-9f2a1c"
	host.publicKeyPin = "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	host.localIPs = []string{"192.168.1.44"}
	host.allowed = []string{"console.davi.social", "davi.social", "localhost:3000", "localhost:3002", "shop.davi.social"}
	host.blocked = []string{"localhost:5173", "staging.example.com"}
	host.settings = settings.Settings{Mode: settings.ModeReadWrite}
	host.seedDevices()
	host.devices = append(host.devices, PairedDevice{
		ID: "dev-3", Name: "Workshop ACR1252U", Platform: "reader",
		PairedAt: time.Now().Add(-96 * time.Hour),
	})
	host.clients = []Client{
		{ID: "c1", Origin: "https://console.davi.social", RemoteAddr: "127.0.0.1:59994",
			UserAgent:   "Mozilla/5.0 (X11; Linux x86_64) Chrome/141.0.0.0 Safari/537.36",
			ConnectedAt: time.Now().Add(-4 * time.Minute)},
		{ID: "c2", Origin: "https://shop.davi.social", RemoteAddr: "127.0.0.1:60004",
			UserAgent:   "Mozilla/5.0 (X11; Linux x86_64) Chrome/141.0.0.0 Safari/537.36",
			ConnectedAt: time.Now().Add(-2 * time.Minute), Writes: 3},
		{ID: "c3", RemoteAddr: "127.0.0.1:60012", UserAgent: "Go-http-client/2.0",
			ConnectedAt: time.Now().Add(-30 * time.Second)},
	}

	ring := logbuf.New(500)
	seedLog(ring)

	console := New(Config{
		Host: host, Logs: ring,
		Name: "davi-nfc-agent", Version: "1.0.3", Dev: true,
	})

	mux := http.NewServeMux()
	mux.Handle("/control/", console.Handler())
	mux.HandleFunc("/ws", fakeTagFeed)
	mux.Handle("/", Console())

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	token, err := console.auth.MintHandoff()
	if err != nil {
		t.Fatal(err)
	}
	if f := os.Getenv("SCREENSHOT_TOKEN_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(token), 0o600)
	}

	fmt.Printf("READY http://%s/control/session?token=%s\n", addr, token)

	// Keep serving until the driver signals it is finished.
	stop := os.Getenv("SCREENSHOT_STOP_FILE")
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if stop != "" {
			if _, err := os.Stat(stop); err == nil {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func seedLog(ring *logbuf.Ring) {
	logger := log.New(ring, "", log.LstdFlags)
	agentLog := log.New(ring, "[agent] ", log.LstdFlags)
	unified := log.New(ring, "[unified] ", log.LstdFlags)
	device := log.New(ring, "[device] ", log.LstdFlags)
	client := log.New(ring, "[client] ", log.LstdFlags)

	logger.Print("Starting davi-nfc-agent 1.0.3")
	logger.Print("Agent public key pin: sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
	logger.Print("[multi] Manager registered: hardware")
	logger.Print("[multi] Manager registered: smartphone")
	agentLog.Print("Auto-selected NFC device: ACS ACR1252U 01 00")
	unified.Print("Starting NFC Agent server on port 9470 (device + client)...")
	unified.Print("Listening on :9470 (TLS)")
	unified.Print("mDNS service registered: _nfc-device._tcp on port 9470")
	client.Print("Client connected: 8f3a1c2d (total: 1)")
	device.Print("Device registered: Ned's iPhone (iOS 17.4)")
	logger.Print("Warning: certificate does not cover 192.168.1.44; clients reaching the agent there cannot verify it")
	agentLog.Print("Tag scanned: 04A2B3C4D5E680 (NTAG215)")
	agentLog.Print("Write verified: 3 records, 78 bytes")
	logger.Print("Origin rejected: staging.example.com is not in the allowlist")
	device.Print("Device heartbeat timeout: Front desk Pixel, marking inactive")
	logger.Print("Error listing NFC devices: failed to establish PC/SC context")
	agentLog.Print("Retrying write after transient failure (attempt 2 of 3)")
	agentLog.Print("Tag scanned: 04A2B3C4D5E680 (NTAG215)")
	client.Print("Client disconnected: 8f3a1c2d (total: 0)")
}

var fakeUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// fakeTagFeed stands in for the client endpoint, which needs real hardware.
func fakeTagFeed(w http.ResponseWriter, r *http.Request) {
	conn, err := fakeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	tag := func(uid, tagType, text string, memory, usable int) map[string]any {
		return map[string]any{
			"uid":        uid,
			"type":       tagType,
			"technology": "ISO14443-3A",
			"scannedAt":  time.Now().Format(time.RFC3339),
			"text":       text,
			"message": map[string]any{
				"records": []map[string]any{
					{"tnf": 1, "type": "U", "uri": text, "payload": "A2Rhdmkuc29jaWFsL3QvOWYyYTFj"},
					{"tnf": 1, "type": "T", "text": "Workshop bench 3", "language": "en", "payload": "AmVuV29ya3Nob3AgYmVuY2ggMw=="},
					{"tnf": 4, "type": "android.com:pkg", "payload": "c29jaWFsLmRhdmkuYXBw"},
				},
			},
			"capabilities": map[string]any{
				"memorySize":          memory,
				"usableCapacity":      usable,
				"writable":            true,
				"lockable":            true,
				"passwordProtectable": true,
				"readOnly":            false,
			},
		}
	}

	// Several distinct tags, so the history table looks like a real run.
	tags := []map[string]any{
		tag("04A2B3C4D5E680", "NTAG215", "https://davi.social/t/9f2a1c", 540, 504),
		tag("04117BE2C15D91", "NTAG213", "https://davi.social/t/4b7e02", 180, 144),
		tag("0453C9A17F2280", "MIFARE Classic 1K", "Bench 7 - calibration", 1024, 716),
		tag("04E81D3B6A4400", "NTAG216", "https://davi.social/t/c30d55", 924, 888),
	}

	send := func(kind string, payload any) error {
		b, _ := json.Marshal(map[string]any{"type": kind, "payload": payload})
		return conn.WriteMessage(websocket.TextMessage, b)
	}

	_ = send("deviceStatus", map[string]any{"connected": true, "message": "Reader ready: ACS ACR1252U", "cardPresent": false})
	_ = send("tagData", tags[0])
	_ = send("deviceStatus", map[string]any{"connected": true, "message": "Tag present", "cardPresent": true})

	// Answer transceive requests, so the APDU console has something to show.
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Payload struct {
					Data string `json:"data"`
				} `json:"payload"`
			}
			if json.Unmarshal(raw, &req) != nil || req.Type != "transceiveRequest" {
				continue
			}

			cmd, _ := base64.StdEncoding.DecodeString(req.Payload.Data)
			body, _ := json.Marshal(map[string]any{
				"id":      req.ID,
				"type":    "transceiveResponse",
				"success": true,
				"payload": map[string]any{"data": base64.StdEncoding.EncodeToString(fakeAPDUReply(cmd))},
			})
			_ = conn.WriteMessage(websocket.TextMessage, body)
		}
	}()

	// A slow trickle so the Live feed and the history both fill up.
	for i := 0; i < 60; i++ {
		time.Sleep(2 * time.Second)
		if i%2 == 0 {
			if err := send("tagData", tags[(i/2)%len(tags)]); err != nil {
				return
			}
		} else if err := send("deviceStatus", map[string]any{"connected": true, "message": "Polling for tags", "cardPresent": true}); err != nil {
			return
		}
	}
}

// fakeAPDUReply answers a handful of commands plausibly enough to exercise the
// console's status-word decoding.
func fakeAPDUReply(cmd []byte) []byte {
	switch {
	case len(cmd) >= 2 && cmd[0] == 0xFF && cmd[1] == 0xCA: // Get UID
		return []byte{0x04, 0xA2, 0xB3, 0xC4, 0xD5, 0xE6, 0x80, 0x90, 0x00}
	case len(cmd) >= 2 && cmd[0] == 0x00 && cmd[1] == 0xA4: // SELECT
		return []byte{0x90, 0x00}
	case len(cmd) >= 2 && cmd[0] == 0x00 && cmd[1] == 0xB0: // READ BINARY
		return []byte{0x00, 0x0F, 0xD1, 0x01, 0x0B, 0x55, 0x01, 0x64, 0x61, 0x76, 0x69, 0x2E, 0x73, 0x6F, 0x63, 0x69, 0x61, 0x6C, 0x90, 0x00}
	case len(cmd) >= 2 && cmd[0] == 0x90 && cmd[1] == 0x60: // DESFire GetVersion
		return []byte{0x04, 0x01, 0x01, 0x01, 0x00, 0x1A, 0x05, 0x91, 0xAF}
	case len(cmd) == 1 && cmd[0] == 0x60: // NTAG GET_VERSION, framing level
		return []byte{0x00, 0x04, 0x04, 0x02, 0x01, 0x00, 0x11, 0x03}
	case len(cmd) == 2 && cmd[0] == 0x30: // Ultralight READ
		return []byte{0x04, 0xA2, 0xB3, 0x8D, 0xC4, 0xD5, 0xE6, 0x80, 0x00, 0x00, 0x00, 0x00, 0xE1, 0x10, 0x3E, 0x00}
	default:
		return []byte{0x6A, 0x82} // File or application not found
	}
}
