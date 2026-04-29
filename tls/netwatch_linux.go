//go:build linux

package tls

import "golang.org/x/sys/unix"

// watchAddrChanges subscribes to a kernel netlink multicast group for
// IPv4/IPv6 address and link state changes. Each received message
// triggers notify(); messages are not parsed because the caller only
// needs a wake-up to re-enumerate hosts.
//
// The function blocks until stop is closed (a sibling goroutine then
// closes the netlink socket, which unblocks Recvfrom with an error and
// returns). On socket setup failure the function returns immediately
// and the channel from addrChangeNotifier simply never emits — the
// periodic poll handles change detection in that case.
func watchAddrChanges(stop <-chan struct{}, notify func()) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_LINK,
	}); err != nil {
		unix.Close(fd)
		return
	}

	go func() {
		<-stop
		// Closing the fd unblocks Recvfrom in the main goroutine.
		unix.Close(fd)
	}()

	buf := make([]byte, 4096)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil || n <= 0 {
			return
		}
		notify()
	}
}
