package agent

import (
	"log"
	"net"
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/clipboard"
)

// serviceHost is the address a menu hands out: the machine's first address, or
// localhost when it reports none. Copied into a phone or a browser, so the most
// broadly reachable one wins; see LocalIPs.
func serviceHost() string {
	if ips := LocalIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "localhost"
}

// serviceAddress joins a host and port, bracketing an IPv6 literal.
func serviceAddress(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// copyValue puts a value on the clipboard and reports what happened, which is
// the only feedback a tray menu has for a copy.
func copyValue(logger *log.Logger, what, value string) {
	if value == "" {
		return
	}

	logf := agentLog.Printf
	if logger != nil {
		logf = logger.Printf
	}

	if err := clipboard.Copy(value); err != nil {
		logf("Failed to copy the %s: %v", what, err)
		return
	}
	logf("Copied the %s to the clipboard", what)
}
