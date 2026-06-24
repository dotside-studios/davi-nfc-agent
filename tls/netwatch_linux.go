//go:build linux

package tls

import (
	"errors"

	"golang.org/x/sys/unix"
)

// watchAddrChanges subscribes to a kernel netlink multicast group for
// IPv4/IPv6 address and link state changes. Each received message
// triggers notify(); messages are not parsed because the caller only
// needs a wake-up to re-enumerate hosts.
//
// The function blocks until stop is closed, then returns promptly. It does
// NOT rely on closing the socket from another goroutine to interrupt a
// blocked Recvfrom — Linux does not guarantee that close() wakes a thread
// already blocked in recvfrom on that fd, so a quiescent socket (no address
// changes) would otherwise hang shutdown forever. Instead the socket is
// given a short receive timeout so the loop wakes periodically to observe
// stop. On socket setup failure the function returns immediately and the
// channel from addrChangeNotifier simply never emits — the periodic poll
// handles change detection in that case.
func watchAddrChanges(stop <-chan struct{}, notify func()) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return
	}
	defer func() { _ = unix.Close(fd) }()

	if err := unix.Bind(fd, &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_LINK,
	}); err != nil {
		return
	}

	// Wake out of Recvfrom periodically so we can observe stop. Without this a
	// socket with no pending messages would block indefinitely.
	timeout := unix.Timeval{Sec: 0, Usec: 200000} // 200ms
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout)

	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			// Receive timeout or interrupted syscall: loop back to re-check stop.
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		if n <= 0 {
			return
		}
		notify()
	}
}
