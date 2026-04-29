//go:build !linux && !darwin && !windows

package tls

// watchAddrChanges has no native event source on this OS, so it simply
// blocks until stop fires and never invokes notify. The caller's
// periodic poll covers change detection.
func watchAddrChanges(stop <-chan struct{}, _ func()) {
	<-stop
}
