// Package netaddr answers the one question everything that hands out an address
// has to answer: what to call this machine.
package netaddr

import (
	"net"
	"strconv"
)

// LocalIPs returns local non-loopback IP addresses, IPv4 first so a caller that
// takes the first gets the most broadly compatible one. Link-local and
// unspecified addresses are skipped: nothing can be told to connect to them.
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

// Host is the address to publish this machine under: its first local IP, or
// localhost when it has none of its own to offer.
func Host() string {
	if ips := LocalIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "localhost"
}

// HostPort joins a host and a port, in brackets for an IPv6 literal.
func HostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
