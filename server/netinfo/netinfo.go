// Package netinfo reports the addresses this machine serves on, for whatever
// hands one out: a tray entry to copy, a console page to display, a pairing
// link for a phone to open.
//
// It answers about the host rather than about any particular listener, so what
// is bound is the caller's to supply.
package netinfo

import (
	"net"
	"strconv"
)

// LocalIPs returns local non-loopback IP addresses (both IPv4 and IPv6
// globals). IPv4 addresses come first so callers that pick ips[0] get the most
// broadly compatible address. Link-local and unspecified addresses are skipped.
func LocalIPs() []string {
	var v4, v6 []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	return append(v4, v6...)
}

// ServiceAddress is host:port for the address this machine is reached on,
// bracketing an IPv6 literal. The host is the first of [LocalIPs], or localhost
// when it reports none: the value is copied into a phone or a browser, so the
// most broadly reachable one wins.
func ServiceAddress(port int) string {
	return net.JoinHostPort(serviceHost(), strconv.Itoa(port))
}

// serviceHost is the address ServiceAddress builds on.
func serviceHost() string {
	if ips := LocalIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "localhost"
}
