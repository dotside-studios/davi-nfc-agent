package netinfo

import (
	"net"
	"strconv"
	"testing"
)

// The address is copied into a phone or a browser, so it has to parse as one:
// an IPv6 host must arrive bracketed, which is what JoinHostPort is for.
func TestServiceAddressParsesBack(t *testing.T) {
	for _, port := range []int{9470, 1, 65535} {
		addr := ServiceAddress(port)

		host, gotPort, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("ServiceAddress(%d) = %q, which does not parse: %v", port, addr, err)
		}
		if gotPort != strconv.Itoa(port) {
			t.Errorf("ServiceAddress(%d) carries port %q", port, gotPort)
		}
		if host == "" {
			t.Errorf("ServiceAddress(%d) = %q, which names no host", port, addr)
		}
	}
}

// A machine reporting no usable address still has to hand out something a
// browser on it can open.
func TestServiceHostFallsBackToLocalhost(t *testing.T) {
	host := serviceHost()
	if host == "" {
		t.Fatal("serviceHost() is empty")
	}

	ips := LocalIPs()
	if len(ips) == 0 {
		if host != "localhost" {
			t.Errorf("serviceHost() = %q with no local addresses, want localhost", host)
		}
		return
	}
	if host != ips[0] {
		t.Errorf("serviceHost() = %q, want the first local address %q", host, ips[0])
	}
}

// The addresses are handed to another machine, so an address that only means
// something on this one is not worth offering.
func TestLocalIPsSkipsAddressesNobodyElseCanReach(t *testing.T) {
	var seenV6 bool
	for _, s := range LocalIPs() {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Errorf("LocalIPs() carried %q, which is not an IP", s)
			continue
		}
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			t.Errorf("LocalIPs() carried %q, which no other machine can reach", s)
		}

		// IPv4 first, so once a v6 address appears no v4 may follow.
		if ip.To4() == nil {
			seenV6 = true
		} else if seenV6 {
			t.Errorf("LocalIPs() put the IPv4 address %q after an IPv6 one", s)
		}
	}
}
