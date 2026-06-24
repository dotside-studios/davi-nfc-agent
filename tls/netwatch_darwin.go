//go:build darwin

package tls

import (
	"errors"

	"golang.org/x/sys/unix"
)

// watchAddrChanges opens a route socket (PF_ROUTE / AF_ROUTE) and treats
// every kernel routing message as a wake-up. macOS posts RTM_IFINFO,
// RTM_NEWADDR, RTM_DELADDR, RTM_IFANNOUNCE etc. to this socket whenever
// interfaces or addresses change. We don't parse the messages — the
// caller only needs to know "something happened" to re-check hosts.
//
// The function blocks until stop is closed, then returns promptly. As on
// Linux, it does not rely on closing the socket from another goroutine to
// interrupt a blocked Read (which is not guaranteed to wake the reader);
// instead a short receive timeout lets the loop observe stop periodically.
func watchAddrChanges(stop <-chan struct{}, notify func()) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return
	}
	defer func() { _ = unix.Close(fd) }()

	// Wake out of Read periodically so we can observe stop. Without this a
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

		n, err := unix.Read(fd, buf)
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
