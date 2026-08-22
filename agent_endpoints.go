package main

import (
	"fmt"
	"net"
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/surface"
)

// The IDs the agent's own endpoints are registered under. A feature publishing
// an address of its own picks an ID of its own; these are the two that come
// with the servers.
const (
	EndpointDevice = "device"
	EndpointClient = "client"
)

// Endpoints is the register of addresses this agent hands out. The servers put
// theirs here as they start and withdraw them as they stop, the pairing plugin
// does the same for its page, and a consumer's own plugin is shown beside them
// without the tray knowing what it serves.
//
// The tray draws whatever is in it, so nothing has to be added there for a new
// address to appear.
func (a *Agent) Endpoints() *surface.Endpoints { return &a.endpoints }

// publishEndpoints registers what the servers are answering on. Called once
// they are up, so the addresses name a port that is actually bound: these are
// pasted into a device, where an address that refuses the connection is worse
// than none.
func (a *Agent) publishEndpoints() {
	client, device := a.serverURLs()

	a.Endpoints().Set(surface.Endpoint{
		ID:      EndpointDevice,
		Label:   "Device",
		URL:     device,
		Tooltip: "Where a phone or a networked reader connects. Click to copy",
	})
	a.Endpoints().Set(surface.Endpoint{
		ID:      EndpointClient,
		Label:   "Client",
		URL:     client,
		Tooltip: "Where a web page connects. Click to copy",
	})
}

// withdrawEndpoints marks the servers' addresses as not running, keeping their
// place on the menu for when they come back.
func (a *Agent) withdrawEndpoints() {
	a.Endpoints().SetURL(EndpointDevice, "")
	a.Endpoints().SetURL(EndpointClient, "")
}

// serverURLs builds the two addresses the single listener answers on. Devices
// and clients share the agent's port; a device asks for the device role with
// ?mode=device, a client opens plain /ws.
func (a *Agent) serverURLs() (client, device string) {
	scheme := "ws"
	if a.CertFile != "" && a.KeyFile != "" {
		scheme = "wss"
	}

	// The port being served, not the one configured, for the same reason the
	// addresses wait for the servers to be up.
	port := a.ServingPort()
	if port == 0 {
		port = DEFAULT_DEVICE_PORT
	}

	client = fmt.Sprintf("%s://%s/ws", scheme, hostPort(localHost(), port))
	return client, client + "?mode=device"
}

// localHost is the address to publish this machine under: the first local IP,
// or localhost when the machine has no address of its own to offer.
func localHost() string {
	if ips := getLocalIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "localhost"
}

// getLocalIPs returns local non-loopback IP addresses (both IPv4 and IPv6 globals).
// IPv4 addresses come first so callers that pick ips[0] get the most broadly
// compatible address. Link-local and unspecified addresses are skipped.
func getLocalIPs() []string {
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

// hostPort joins a host and port using bracket notation for IPv6 literals.
func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
