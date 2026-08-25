//go:build !nowebui

package console

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
)

// Server serves the console's privileged API. See auth.go for the gate.
//
// Tag reading and writing are absent by design: the console does those over the
// ordinary client endpoint, so there is one implementation of the write path.
type Server struct {
	host      Host
	adapter   *host // set when the Host is this package's own agent adapter
	auth      *Auth
	logs      *logbuf.Ring
	name      string
	version   string
	dev       bool
	startedAt time.Time

	mu        sync.Mutex
	listeners map[int]chan struct{}
	listenerN int
}

// newServer builds the console API over a host.
func newServer(config serverConfig) *Server {
	return &Server{
		host:      config.Host,
		auth:      NewAuth(),
		logs:      config.Logs,
		name:      config.Name,
		version:   config.Version,
		dev:       config.Dev,
		startedAt: time.Now(),
		listeners: make(map[int]chan struct{}),
	}
}

// Handler returns the control routes. Everything but the session handoff runs
// through requireSession; the handoff is what a browser arrives at without a
// session and carries its own single-use credential.
func (c *Server) Handler() http.Handler {
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
func (c *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reason := c.auth.authorize(r); reason != "" {
			log.Printf("Control request refused (%s): %s %s", reason, r.Method, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSession exchanges a tray-minted handoff token for a session cookie.
func (c *Server) handleSession(w http.ResponseWriter, r *http.Request) {
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
		Name:     cookieName,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		// Unconditional Secure would drop the cookie on a plain-HTTP agent.
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	// Redirect so the spent token leaves the address bar and the history.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (c *Server) handleSignout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		c.auth.RevokeSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.buildState())
}

// handleLogs returns buffered log entries, optionally only those after a
// sequence the caller already has.
func (c *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
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

// action is a console request to change something.
type action struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (c *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req action
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
func (c *Server) NotifyChange() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.listeners {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (c *Server) subscribe() (<-chan struct{}, func()) {
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

// upgrader upgrades the console's live connection. CheckOrigin is the
// strict same-origin test, never the agent's allowlist.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 8192,
	CheckOrigin: func(r *http.Request) bool {
		return isLoopbackRequest(r) && isSameOriginRequest(r)
	},
}

// envelope is one message pushed to the console.
type envelope struct {
	Type  string         `json:"type"`
	State *State         `json:"state,omitempty"`
	Logs  []logbuf.Entry `json:"logs,omitempty"`
}

// handleWS streams state changes and log lines to the console.
func (c *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Control WebSocket upgrade failed: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	changes, unsubscribe := c.subscribe()
	defer unsubscribe()

	var logCh <-chan logbuf.Entry
	var logCancel func()
	if c.logs != nil {
		logCh, logCancel = c.logs.Subscribe(512)
		defer logCancel()
	}

	// Gorilla permits one concurrent writer; this connection has three sources.
	out := make(chan envelope, 64)
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
	out <- envelope{Type: "state", State: &state}
	if c.logs != nil {
		if entries := c.logs.Entries(); len(entries) > 0 {
			out <- envelope{Type: "logs", Logs: entries}
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
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(env); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// pumpEvents turns change signals and log entries into outbound envelopes,
// batching log lines on a short tick.
func (c *Server) pumpEvents(changes <-chan struct{}, logs <-chan logbuf.Entry, out chan<- envelope, done <-chan struct{}) {
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()

	var pending []logbuf.Entry

	send := func(env envelope) bool {
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
			if !send(envelope{Type: "state", State: &state}) {
				return
			}

		case entry, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			pending = append(pending, entry)
			if len(pending) >= 200 {
				if !send(envelope{Type: "logs", Logs: pending}) {
					return
				}
				pending = nil
			}

		case <-flush.C:
			if len(pending) > 0 {
				if !send(envelope{Type: "logs", Logs: pending}) {
					return
				}
				pending = nil
			}
		}
	}
}

// ConsoleURL returns the console address carrying a fresh single-use token.
func (c *Server) ConsoleURL() (string, error) {
	token, err := c.auth.MintHandoff()
	if err != nil {
		return "", err
	}

	scheme := "http"
	if c.host.TLSEnabled() {
		scheme = "https"
	}

	// Always loopback; the control surface refuses anything else.
	return fmt.Sprintf("%s://%s/control/session?token=%s",
		scheme, net.JoinHostPort("localhost", strconv.Itoa(c.host.Port())), token), nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
