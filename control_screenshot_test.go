package main

// Screenshot harness. Stands up the real control handler and the embedded
// console against a seeded agent, so the UI can be captured without hardware.
//
// Not part of the normal suite: it only runs when SCREENSHOT_ADDR is set.
//
//	SCREENSHOT_ADDR=127.0.0.1:9911 go test -run TestScreenshotHarness -timeout 20m .

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
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
)

func TestScreenshotHarness(t *testing.T) {
	addr := os.Getenv("SCREENSHOT_ADDR")
	if addr == "" {
		t.Skip("set SCREENSHOT_ADDR to run the screenshot harness")
	}

	dir := t.TempDir()

	agent := NewAgent(nfc.NewMockManager())
	agent.ConfigDir = dir
	agent.DevicePort = 9470
	agent.APISecret = "s3cr3t-0f-the-agent-9f2a1c"
	agent.PublicKeyPin = "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

	origins, err := NewOriginStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	origins.Allow("console.davi.social")
	origins.Allow("localhost:3002")
	origins.RecordBlocked("staging.example.com")
	origins.RecordBlocked("localhost:5173")
	agent.Origins = origins

	devices, err := NewDeviceRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ name, platform string }{
		{"Ned's iPhone", "iOS 17.4"},
		{"Front desk Pixel", "Android 14"},
		{"Workshop ACR1252U", "reader"},
	} {
		if _, _, err := devices.Pair(d.name, d.platform); err != nil {
			t.Fatal(err)
		}
	}
	agent.Devices = devices

	settings, err := NewSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ring := logbuf.New(500)
	seedLog(ring)

	auth := NewControlAuth()
	control := NewControlServer(agent, auth, settings, ring, nil, 9472)

	// A real client server, so the connected-clients section has rows. The tag
	// feed is still stubbed; this only supplies the session bookkeeping.
	agent.ClientServer = clientserver.New(clientserver.Config{
		AllowedOrigins: []string{"*"},
		OnChange:       control.NotifyChange,
	}, server.NewServerBridge())

	mux := http.NewServeMux()
	mux.Handle("/control/", control.Handler())
	mux.HandleFunc("/ws", fakeTagFeed)
	mux.HandleFunc("/ws-real", agent.ClientServer.ServeWS)
	mux.Handle("/", webUIHandler())

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	token, err := auth.MintHandoff()
	if err != nil {
		t.Fatal(err)
	}
	if f := os.Getenv("SCREENSHOT_TOKEN_FILE"); f != "" {
		os.WriteFile(f, []byte(token), 0o600)
	}

	// A couple of stand-in applications, one of them writing.
	go fakeClients(addr)

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

// fakeClients connects a few stand-in applications to the real client server.
func fakeClients(addr string) {
	time.Sleep(time.Second)

	for _, c := range []struct {
		origin string
		writes int
	}{
		{"https://console.davi.social", 0},
		{"https://shop.davi.social", 3},
		{"", 0}, // a non-browser caller, which has no Origin
	} {
		header := http.Header{}
		if c.origin != "" {
			header.Set("Origin", c.origin)
			header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Chrome/141.0.0.0 Safari/537.36")
		} else {
			header.Set("User-Agent", "Go-http-client/2.0")
		}

		conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws-real", header)
		if err != nil {
			continue
		}
		for i := 0; i < c.writes; i++ {
			_ = conn.WriteJSON(map[string]any{
				"type":    "writeRequest",
				"payload": map[string]any{"records": []map[string]any{{"type": "text", "content": "x"}}},
			})
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
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

var fakeUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// fakeTagFeed stands in for the client endpoint, which needs real hardware.
func fakeTagFeed(w http.ResponseWriter, r *http.Request) {
	conn, err := fakeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

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

	send("deviceStatus", map[string]any{"connected": true, "message": "Reader ready: ACS ACR1252U", "cardPresent": false})
	send("tagData", tags[0])
	send("deviceStatus", map[string]any{"connected": true, "message": "Tag present", "cardPresent": true})

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
