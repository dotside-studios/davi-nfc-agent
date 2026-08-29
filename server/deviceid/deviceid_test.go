package deviceid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/server/deviceid"
)

func request() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/ws", nil)
}

func TestOfReportsWhatWithStored(t *testing.T) {
	got := deviceid.Of(deviceid.With(request(), "device-7"))
	if got != "device-7" {
		t.Fatalf("Of = %q, want %q", got, "device-7")
	}
}

func TestOfIsEmptyWhenNothingAdmitted(t *testing.T) {
	if got := deviceid.Of(request()); got != "" {
		t.Fatalf("Of on an untouched request = %q, want empty", got)
	}
}

// An empty identity names nobody, so it must not be stored: a backend cannot
// tell it apart from a request nothing admitted, and should not try.
func TestWithEmptyIdentityLeavesTheRequestAlone(t *testing.T) {
	r := request()
	if got := deviceid.With(r, ""); got != r {
		t.Fatal("With(r, \"\") returned a different request")
	}
	if got := deviceid.Of(deviceid.With(r, "")); got != "" {
		t.Fatalf("Of = %q, want empty", got)
	}
}

// The identity replaces rather than accumulates: whatever admitted the request
// last is who it is.
func TestWithReplacesAnEarlierIdentity(t *testing.T) {
	r := deviceid.With(deviceid.With(request(), "first"), "second")
	if got := deviceid.Of(r); got != "second" {
		t.Fatalf("Of = %q, want %q", got, "second")
	}
}

func TestNilRequestIsTolerated(t *testing.T) {
	if got := deviceid.With(nil, "device-7"); got != nil {
		t.Fatal("With(nil, ...) did not return nil")
	}
	if got := deviceid.Of(nil); got != "" {
		t.Fatalf("Of(nil) = %q, want empty", got)
	}
}
