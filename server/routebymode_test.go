package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

func mark(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
}

// The device and client endpoints share one path, so the routing here decides
// which of them a connection reaches.
func TestRouteByMode(t *testing.T) {
	h := server.RouteByMode(mark(200), map[string]http.Handler{
		server.ModeDevice: mark(299),
	})

	cases := []struct {
		name string
		req  func() *http.Request
		want int
	}{
		{"query names the device mode", func() *http.Request {
			return httptest.NewRequest("GET", "/ws?mode=device", nil)
		}, 299},
		{"header is the older way to say the same", func() *http.Request {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.Header.Set("X-Device-Mode", "true")
			return r
		}, 299},
		{"no mode is a client", func() *http.Request {
			return httptest.NewRequest("GET", "/ws", nil)
		}, 200},
		{"an unknown mode falls back rather than failing", func() *http.Request {
			return httptest.NewRequest("GET", "/ws?mode=banana", nil)
		}, 200},
		{"the header must say true", func() *http.Request {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.Header.Set("X-Device-Mode", "yes")
			return r
		}, 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, c.req())
			if rec.Code != c.want {
				t.Errorf("code = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

// server does not import the driver, so the two definitions of "this is a
// device" could drift apart. They must agree on every form.
func TestRouteByModeAgreesWithTheDriver(t *testing.T) {
	device := server.RouteByMode(mark(200), map[string]http.Handler{server.ModeDevice: mark(299)})

	reqs := map[string]*http.Request{
		"query":  httptest.NewRequest("GET", "/ws?mode=device", nil),
		"header": httptest.NewRequest("GET", "/ws", nil),
		"client": httptest.NewRequest("GET", "/ws", nil),
	}
	reqs["header"].Header.Set("X-Device-Mode", "true")

	for name, r := range reqs {
		rec := httptest.NewRecorder()
		device.ServeHTTP(rec, r)
		routedToDevice := rec.Code == 299

		if got := remotenfc.IsDeviceConnection(r); got != routedToDevice {
			t.Errorf("%s: driver says device=%v, routing says %v", name, got, routedToDevice)
		}
	}
}
