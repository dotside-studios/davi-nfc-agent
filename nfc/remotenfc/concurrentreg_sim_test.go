package remotenfc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceid"
	"github.com/gorilla/websocket"
)

// TestPhone_ConcurrentSameIDRegistrationConvergesToOneSession: several
// connections that resolve to the SAME device identity register at once (a phone
// whose credential maps to a fixed device ID opening connections in quick
// succession, or a flaky link reconnecting before the old socket is torn down).
// Registration removes the session it replaces and installs its own, but the
// check and the install are separate lock acquisitions, so concurrent
// registrations for one identity can each miss the others and leave several live
// connections mapped to the one device — orphaned sessions that no operation can
// reach and whose teardown never runs. Afterwards there must be exactly one
// session for the identity, with the reverse map holding only it.
func TestPhone_ConcurrentSameIDRegistrationConvergesToOneSession(t *testing.T) {
	const id = "shared-device"
	m := NewManager(DeviceTimeout)
	t.Cleanup(m.Close)

	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, deviceid.With(r, id))
		})
	}
	ts := httptest.NewServer(wrap(m.Handler(ServerOptions{})))
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	const n = 12
	var wg sync.WaitGroup
	conns := make([]*websocket.Conn, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				return
			}
			_ = c.WriteJSON(protocol.WebSocketRequest{
				Type: WSTypeHello,
				Payload: map[string]any{
					"protocolVersion": DeviceProtocolV1,
					"deviceName":      "D",
					"platform":        "android",
				},
			})
			conns[i] = c
		}(i)
	}
	wg.Wait()

	// Wait for the registrations and any replaced-session teardowns to settle.
	var sessions, reverse int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.sessionsMu.RLock()
		sessions, reverse = len(m.sessions), len(m.sessionConn)
		m.sessionsMu.RUnlock()
		if sessions == 1 && reverse == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}

	if sessions != 1 {
		t.Errorf("after %d concurrent registrations for one identity, %d live sessions; want exactly 1", n, sessions)
	}
	if reverse != 1 {
		t.Errorf("after %d concurrent registrations for one identity, %d entries in the reverse session map; want 1 — the extras are orphaned connections that were never replaced or cleaned up", n, reverse)
	}
}
