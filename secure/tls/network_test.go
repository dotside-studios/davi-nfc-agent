package tls

import (
	"testing"
)

func TestGetLANIPs(t *testing.T) {
	ips, err := GetLANIPs()
	if err != nil {
		t.Fatalf("GetLANIPs failed: %v", err)
	}

	t.Logf("Found LAN IPs: %v", ips)

	// Should find at least something on most systems
	// (though this could fail in isolated containers)
	if len(ips) == 0 {
		t.Log("Warning: No LAN IPs found (may be expected in some environments)")
	}
}

func TestGetAllHosts(t *testing.T) {
	hosts, err := GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts failed: %v", err)
	}

	t.Logf("Hosts for certificate: %v", hosts)

	// Should always include localhost, 127.0.0.1, and ::1
	if len(hosts) < 3 {
		t.Error("Expected at least localhost, 127.0.0.1, and ::1")
	}

	want := map[string]bool{"localhost": false, "127.0.0.1": false, "::1": false}
	for _, h := range hosts {
		if _, ok := want[h]; ok {
			want[h] = true
		}
	}
	for h, found := range want {
		if !found {
			t.Errorf("Expected %q in hosts", h)
		}
	}
}
