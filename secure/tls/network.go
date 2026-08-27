// Package tls provides automatic TLS certificate management with cross-platform trust store installation.
package tls

import (
	"net"
)

// GetLANIPs returns local non-loopback IP addresses (both IPv4 and IPv6 globals).
// IPv6 link-local addresses (fe80::/10) and unspecified addresses are skipped because
// they are not routable across hosts and would clutter certificate SANs.
func GetLANIPs() ([]string, error) {
	var ips []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		// Skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			ips = append(ips, ip.String())
		}
	}

	return ips, nil
}

// GetAllHosts returns localhost + LAN IPs for certificate generation.
// Includes both IPv4 (127.0.0.1) and IPv6 (::1) loopback so certs validate
// regardless of which family the client resolves localhost to.
func GetAllHosts() ([]string, error) {
	hosts := []string{"localhost", "127.0.0.1", "::1"}

	lanIPs, err := GetLANIPs()
	if err != nil {
		return hosts, err
	}

	hosts = append(hosts, lanIPs...)
	return hosts, nil
}
