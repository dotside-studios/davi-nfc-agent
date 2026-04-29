//go:build darwin

package tls

import "golang.org/x/sys/unix"

// watchAddrChanges opens a route socket (PF_ROUTE / AF_ROUTE) and treats
// every kernel routing message as a wake-up. macOS posts RTM_IFINFO,
// RTM_NEWADDR, RTM_DELADDR, RTM_IFANNOUNCE etc. to this socket whenever
// interfaces or addresses change. We don't parse the messages — the
// caller only needs to know "something happened" to re-check hosts.
//
// As on Linux, a sibling goroutine closes the fd when stop fires, which
// unblocks the Read.
func watchAddrChanges(stop <-chan struct{}, notify func()) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return
	}

	go func() {
		<-stop
		unix.Close(fd)
	}()

	buf := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil || n <= 0 {
			return
		}
		notify()
	}
}
