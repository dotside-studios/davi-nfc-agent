package tls

// addrChangeNotifier returns a channel that emits whenever the OS reports
// a network address or interface change. The channel is buffered (size 1)
// and coalesces bursts of events; consumers should treat each receive as
// "something changed, re-check."
//
// The watcher goroutine exits and the channel is closed when stop is
// closed. Implementations are per-OS in netwatch_<goos>.go and use native
// APIs (netlink on Linux, route socket on Darwin, NotifyAddrChange on
// Windows). On unsupported OSes the channel never emits — callers should
// keep a periodic poll as a safety net.
func addrChangeNotifier(stop <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)
		watchAddrChanges(stop, func() {
			select {
			case out <- struct{}{}:
			default:
				// Already pending — coalesce.
			}
		})
	}()
	return out
}
