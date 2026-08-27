package agent

import "net"

// LocalIPs returns local non-loopback IP addresses (both IPv4 and IPv6 globals).
// IPv4 addresses come first so callers that pick ips[0] get the most broadly
// compatible address. Link-local and unspecified addresses are skipped.
//
// It lives here rather than with the tray because the console reports the same
// addresses, and the tray is one front end among several.
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
