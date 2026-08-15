//go:build !nocontrol

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// ControlServer serves the console's privileged API. See control_auth.go for
// the gate.
//
// Tag reading and writing are absent by design: the console does those over the
// ordinary client endpoint, so there is one implementation of the write path.
type ControlServer struct {
	agent         *Agent
	auth          *ControlAuth
	settings      *SettingsStore
	logs          *logbuf.Ring
	bootstrap     *tls.BootstrapServer
	bootstrapPort int
	startedAt     time.Time

	// Set by the tray so an action taken in the console runs through the same
	// code. A nil hook means the action is unavailable.
	OnStart        func() error
	OnStop         func()
	OnSelectDevice func(devicePath string) error
	OnSettings     func(Settings)
	OnQuit         func()

	mu        sync.Mutex
	listeners map[int]chan struct{}
	listenerN int
}

// NewControlServer wires the console API to a running agent.
func NewControlServer(agent *Agent, auth *ControlAuth, settings *SettingsStore, logs *logbuf.Ring, bootstrap *tls.BootstrapServer, bootstrapPort int) *ControlServer {
	return &ControlServer{
		agent:         agent,
		auth:          auth,
		settings:      settings,
		logs:          logs,
		bootstrap:     bootstrap,
		bootstrapPort: bootstrapPort,
		startedAt:     time.Now(),
		listeners:     make(map[int]chan struct{}),
	}
}

// Handler returns the control routes. Everything but the session handoff runs
// through requireSession; the handoff is what a browser arrives at without a
// session and carries its own single-use credential.
func (c *ControlServer) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/control/session", c.handleSession)
	mux.Handle("/control/signout", c.requireSession(http.HandlerFunc(c.handleSignout)))
	mux.Handle("/control/state", c.requireSession(http.HandlerFunc(c.handleState)))
	mux.Handle("/control/logs", c.requireSession(http.HandlerFunc(c.handleLogs)))
	mux.Handle("/control/action", c.requireSession(http.HandlerFunc(c.handleAction)))
	mux.Handle("/control/ws", c.requireSession(http.HandlerFunc(c.handleWS)))

	return mux
}

// requireSession enforces the control gate. Deliberately without CORS headers,
// unlike the client routes.
func (c *ControlServer) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reason := c.auth.authorizeControlRequest(r); reason != "" {
			log.Printf("Control request refused (%s): %s %s", reason, r.Method, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSession exchanges a tray-minted handoff token for a session cookie.
func (c *ControlServer) handleSession(w http.ResponseWriter, r *http.Request) {
	// Checked before redeeming, so a refused request cannot burn the token.
	if !isLoopbackRequest(r) || !isSameOriginRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	session, ok := c.auth.RedeemHandoff(r.URL.Query().Get("token"))
	if !ok {
		http.Error(w, "This control-center link has expired or was already used.\n\nOpen the Control Center from the agent's tray menu to get a fresh one.", http.StatusForbidden)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     controlCookieName,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		// Unconditional Secure would drop the cookie on a plain-HTTP agent.
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(controlSessionTTL.Seconds()),
	})

	// Redirect so the spent token leaves the address bar and the history.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (c *ControlServer) handleSignout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(controlCookieName); err == nil {
		c.auth.RevokeSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     controlCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.buildState())
}

// handleLogs returns buffered log entries, optionally only those after a
// sequence the caller already has.
func (c *ControlServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	if c.logs == nil {
		writeJSON(w, http.StatusOK, []logbuf.Entry{})
		return
	}

	var since uint64
	if raw := r.URL.Query().Get("since"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			since = n
		}
	}

	entries := c.logs.Since(since)
	if entries == nil {
		entries = []logbuf.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// controlAction is a console request to change something.
type controlAction struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (c *ControlServer) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req controlAction
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "malformed request"})
		return
	}

	result, err := c.dispatch(req)
	if err != nil {
		log.Printf("Control action %q failed: %v", req.Action, err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	c.NotifyChange()

	response := map[string]any{"ok": true}
	if result != nil {
		response["result"] = result
	}
	writeJSON(w, http.StatusOK, response)
}

// NotifyChange pushes fresh state to connected consoles. Safe from any
// goroutine, and coalesces.
func (c *ControlServer) NotifyChange() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.listeners {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (c *ControlServer) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	c.mu.Lock()
	id := c.listenerN
	c.listenerN++
	c.listeners[id] = ch
	c.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.listeners, id)
			c.mu.Unlock()
		})
	}
}

// controlUpgrader upgrades the console's live connection. CheckOrigin is the
// strict same-origin test, never the agent's allowlist.
var controlUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 8192,
	CheckOrigin: func(r *http.Request) bool {
		return isLoopbackRequest(r) && isSameOriginRequest(r)
	},
}

// controlEnvelope is one message pushed to the console.
type controlEnvelope struct {
	Type  string         `json:"type"`
	State *ControlState  `json:"state,omitempty"`
	Logs  []logbuf.Entry `json:"logs,omitempty"`
}

// handleWS streams state changes and log lines to the console.
func (c *ControlServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := controlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Control WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	changes, unsubscribe := c.subscribe()
	defer unsubscribe()

	var logCh <-chan logbuf.Entry
	var logCancel func()
	if c.logs != nil {
		logCh, logCancel = c.logs.Subscribe(512)
		defer logCancel()
	}

	// Gorilla permits one concurrent writer; this connection has three sources.
	out := make(chan controlEnvelope, 64)
	done := make(chan struct{})

	go func() {
		defer close(done)
		// Reads only to observe the peer closing; the console sends nothing.
		conn.SetReadLimit(4 << 10)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	state := c.buildState()
	out <- controlEnvelope{Type: "state", State: &state}
	if c.logs != nil {
		if entries := c.logs.Entries(); len(entries) > 0 {
			out <- controlEnvelope{Type: "logs", Logs: entries}
		}
	}

	go c.pumpEvents(changes, logCh, out, done)

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-done:
			return
		case env := <-out:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(env); err != nil {
				return
			}
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// pumpEvents turns change signals and log entries into outbound envelopes,
// batching log lines on a short tick.
func (c *ControlServer) pumpEvents(changes <-chan struct{}, logs <-chan logbuf.Entry, out chan<- controlEnvelope, done <-chan struct{}) {
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()

	var pending []logbuf.Entry

	send := func(env controlEnvelope) bool {
		select {
		case out <- env:
			return true
		case <-done:
			return false
		default:
			// Console is behind. Dropping beats blocking the agent: the next
			// state push is a full snapshot and logs stay fetchable by sequence.
			return true
		}
	}

	for {
		select {
		case <-done:
			return

		case <-changes:
			state := c.buildState()
			if !send(controlEnvelope{Type: "state", State: &state}) {
				return
			}

		case entry, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			pending = append(pending, entry)
			if len(pending) >= 200 {
				if !send(controlEnvelope{Type: "logs", Logs: pending}) {
					return
				}
				pending = nil
			}

		case <-flush.C:
			if len(pending) > 0 {
				if !send(controlEnvelope{Type: "logs", Logs: pending}) {
					return
				}
				pending = nil
			}
		}
	}
}

// remoteManager returns the remote device manager, held either directly or
// behind the multi-manager.
func (c *ControlServer) remoteManager() *remotenfc.Manager {
	if c.agent.Manager == nil {
		return nil
	}
	if m, ok := c.agent.Manager.(*remotenfc.Manager); ok {
		return m
	}
	if mm, ok := c.agent.Manager.(interface {
		GetManager(string) (nfc.Manager, bool)
	}); ok {
		if mgr, exists := mm.GetManager(nfc.ManagerTypeSmartphone); exists {
			if m, ok := mgr.(*remotenfc.Manager); ok {
				return m
			}
		}
	}
	return nil
}

// ConsoleURL returns the console address carrying a fresh single-use token.
func (c *ControlServer) ConsoleURL() (string, error) {
	token, err := c.auth.MintHandoff()
	if err != nil {
		return "", err
	}

	scheme := "http"
	if c.agent.CertFile != "" && c.agent.KeyFile != "" {
		scheme = "https"
	}

	// Always loopback; the control surface refuses anything else.
	return fmt.Sprintf("%s://%s/control/session?token=%s",
		scheme, hostPort("localhost", c.agent.DevicePort), token), nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func platformName() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
