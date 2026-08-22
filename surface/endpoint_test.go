package surface_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/surface"
)

func TestEndpointsKeepTheirPlaceWhenTheyChange(t *testing.T) {
	var endpoints surface.Endpoints

	endpoints.Set(surface.Endpoint{ID: "device", Label: "Device", URL: "ws://host/ws?mode=device"})
	endpoints.Set(surface.Endpoint{ID: "client", Label: "Client", URL: "ws://host/ws"})
	endpoints.Set(surface.Endpoint{ID: "pairing", Label: "Pair Phone", URL: "http://host/?pin=111111"})

	// A PIN rotation is the same endpoint at a new address, and must not send
	// the pairing entry to the bottom of the menu.
	endpoints.Set(surface.Endpoint{ID: "pairing", Label: "Pair Phone", URL: "http://host/?pin=222222"})

	list := endpoints.List()
	if len(list) != 3 {
		t.Fatalf("registered %d endpoints, want 3", len(list))
	}
	want := []string{"device", "client", "pairing"}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("endpoints read %v, want them in the order they were registered: %v", ids(list), want)
		}
	}
	if got := list[2].URL; got != "http://host/?pin=222222" {
		t.Errorf("pairing URL = %q, want the one it was last set to", got)
	}
}

func TestEndpointsReportOnlyRealChanges(t *testing.T) {
	var endpoints surface.Endpoints

	changes := 0
	endpoints.OnChange(func([]surface.Endpoint) { changes++ })

	device := surface.Endpoint{ID: "device", Label: "Device", URL: "ws://host/ws?mode=device"}
	endpoints.Set(device)
	// A restart that lands on the same port publishes the same address, which
	// is not a change and must not redraw the menu.
	endpoints.Set(device)

	if changes != 1 {
		t.Fatalf("Changed raised %d times, want 1", changes)
	}

	endpoints.SetURL("device", "")
	if changes != 2 {
		t.Fatalf("withdrawing an address raised %d changes, want 2", changes)
	}
	if endpoint, _ := endpoints.Get("device"); endpoint.Running() {
		t.Error("an endpoint with no URL still reads as running")
	}
	if endpoint, _ := endpoints.Get("device"); endpoint.Label != "Device" {
		t.Errorf("withdrawing the address lost the label: %q", endpoint.Label)
	}
}

func TestEndpointsWithoutAnID(t *testing.T) {
	var endpoints surface.Endpoints

	// It could never be replaced or withdrawn again, so it is not taken.
	endpoints.Set(surface.Endpoint{Label: "Nameless", URL: "http://host/"})

	if endpoints.Len() != 0 {
		t.Fatalf("registered %d endpoints, want none", endpoints.Len())
	}
}

func TestEndpointsRemove(t *testing.T) {
	var endpoints surface.Endpoints

	endpoints.Set(surface.Endpoint{ID: "device", Label: "Device"})
	endpoints.Set(surface.Endpoint{ID: "turnstile", Label: "Turnstile"})

	if !endpoints.Remove("device") {
		t.Fatal("Remove reported nothing to remove")
	}
	if endpoints.Remove("device") {
		t.Error("Remove reported removing the same endpoint twice")
	}
	if list := endpoints.List(); len(list) != 1 || list[0].ID != "turnstile" {
		t.Fatalf("endpoints read %v, want just the turnstile", ids(list))
	}
}

func ids(list []surface.Endpoint) []string {
	out := make([]string, 0, len(list))
	for _, endpoint := range list {
		out = append(out, endpoint.ID)
	}
	return out
}
